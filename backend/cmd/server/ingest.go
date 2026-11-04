package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Push ingestion for the Veeam Enterprise Manager SQL collector.
//
// Why this exists: the EM REST API on the affected Veeam EM 12.0 build never calls
// usp.Security.AddLoggedUserAccounts, so every logon ends in 401. A read-only
// collector on the Veeam host queries VeeamBackupReporting and pushes the
// result here instead. No credentials are stored on either side; the collector
// runs as NT AUTHORITY\SYSTEM and authenticates to SQL over local shared memory.
//
// The collector never writes to the Veeam side, and this endpoint never accepts
// commands for it: the payload is inventory plus session history only.

type ingestServer struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	DBInstanceID string `json:"dbInstanceId"`
}

type ingestJob struct {
	UID             string     `json:"uid"`
	Name            string     `json:"name"`
	Type            int        `json:"type"`
	ServerID        string     `json:"serverId"`
	ScheduleEnabled bool       `json:"scheduleEnabled"`
	LastResult      int        `json:"lastResult"`
	LastRun         *time.Time `json:"lastRun"`
}

type ingestSession struct {
	ID        string     `json:"id"`
	JobName   string     `json:"jobName"`
	JobType   int        `json:"jobType"`
	ServerID  string     `json:"serverId"`
	Platform  int        `json:"platform"`
	Result    int        `json:"result"`
	State     int        `json:"state"`
	Reason    string     `json:"reason"`
	StartedAt *time.Time `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt"`
	// SizeBytes is the size actually written to the repository
	// (C.Backup.Model.BackupJobSessions.stored_size), not the VM's provisioned
	// size — total_size/processed_size run ~26x larger and would misreport
	// capacity. Older collectors omit it, hence the zero default.
	SizeBytes int64 `json:"sizeBytes"`
}

type ingestTask struct {
	VMName      string     `json:"vmName"`
	VMUID       string     `json:"vmUid"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason"`
	StartedAt   *time.Time `json:"startedAt"`
	LastSuccess *time.Time `json:"lastSuccess"`
	JobName     string     `json:"jobName"`
	ServerID    string     `json:"serverId"`
	SizeBytes   int64      `json:"sizeBytes"`
}

type ingestPayload struct {
	CollectorVersion string          `json:"collectorVersion"`
	GeneratedAt      time.Time       `json:"generatedAt"`
	Servers          []ingestServer  `json:"servers"`
	Jobs             []ingestJob     `json:"jobs"`
	Sessions         []ingestSession `json:"sessions"`
	// Tasks is Option B: one entry per VM with its newest object-level run.
	// Absent from 1.0.2 collectors, hence the nil default.
	Tasks []ingestTask `json:"tasks"`
	// Repos (1.0.5+): Veeam repository capacity from Rest.RepositoriesView.
	Repos []ingestRepo `json:"repos"`
}

type ingestRepo struct {
	Name       string `json:"name"`
	Server     string `json:"server"`
	Type       int    `json:"type"`
	TotalBytes int64  `json:"totalBytes"`
	FreeBytes  int64  `json:"freeBytes"`
}

// Veeam result codes observed in C.Backup.Model.JobSessions (7 day sample):
//  0 -> success, 1 -> warning, 2 -> failed, -1 -> in progress / unknown.
func veeamResultName(r int) string {
	switch r {
	case 0:
		return "success"
	case 1:
		return "warning"
	case 2:
		return "failed"
	}
	return "warning"
}

// Only real backup and replica jobs count toward the KPI. Type 100 (Backup
// Configuration), 22000, and the internal job types (18, 19, 21, 10001, 23000)
// are EM housekeeping and would inflate the numbers.
func isBackupJobType(t int) bool { return t == 0 || t == 1 }

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// ingestAuth resolves the source bound to a push token. Tokens are stored hashed.
func ingestAuth(ctx context.Context, db *pgxpool.Pool, token string) (string, bool) {
	if token == "" {
		return "", false
	}
	var srcID string
	err := db.QueryRow(ctx, `SELECT source_id::text FROM ingest_tokens
		WHERE token_hash=$1 AND NOT revoked`, tokenHash(token)).Scan(&srcID)
	if err != nil {
		return "", false
	}
	db.Exec(ctx, `UPDATE ingest_tokens SET last_used_at=now() WHERE token_hash=$1`, tokenHash(token))
	return srcID, true
}

func (a *App) ingestVeeam(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	srcID, ok := ingestAuth(ctx, a.db, bearerToken(r))
	if !ok {
		apiErr(w, 401, "unauthorized", "invalid token")
		return
	}
	var platform string
	var enabled bool
	if err := a.db.QueryRow(ctx, `SELECT platform,enabled FROM backup_sources WHERE id=$1`, srcID).Scan(&platform, &enabled); err != nil {
		apiErr(w, 404, "not_found", "source gone")
		return
	}
	if platform != "veeam_push" {
		apiErr(w, 409, "wrong_platform", "source is not a veeam_push source")
		return
	}
	if !enabled {
		apiErr(w, 409, "disabled", "source disabled")
		return
	}

	var in ingestPayload
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
	if err := dec.Decode(&in); err != nil {
		apiErr(w, 400, "bad_json", "invalid payload")
		return
	}

	// Pick up node alias edits made since the last poll (TTL 5 min).
	nodes.refreshIfStale(ctx, a.db)

	serverName := map[string]string{}
	serverVer := map[string]string{}
	for _, s := range in.Servers {
		if s.ID == "" {
			continue
		}
		serverName[s.ID] = s.Name
		serverVer[s.ID] = s.Version
		// Jobs and sessions carry db_instance_id, not BackupServers.id, so map
		// that too — otherwise every row falls back to a raw GUID.
		if s.DBInstanceID != "" {
			serverName[s.DBInstanceID] = s.Name
			serverVer[s.DBInstanceID] = s.Version
		}
	}

	nJobs, nSess, nSkip := 0, 0, 0
	var warns []string

	for _, j := range in.Jobs {
		if j.UID == "" || j.Name == "" || !isBackupJobType(j.Type) {
			nSkip++
			continue
		}
		jt := "backup"
		if j.Type == 1 {
			jt = "replica"
		}
		srv := serverName[j.ServerID]
		if srv == "" {
			srv = j.ServerID
		}
		srv = nodeName(srv)
		raw, _ := json.Marshal(map[string]any{
			"type": j.Type, "serverId": j.ServerID, "serverVersion": serverVer[j.ServerID],
		})
		if _, err := a.db.Exec(ctx, `INSERT INTO veeam_jobs
			(source_id,uid,name,job_type,backup_server,platform,is_disabled,last_result,last_run,raw,backup_server_id,schedule_enabled,last_result_raw,server_name,updated_at)
			VALUES($1,$2,$3,$4,$5,'veeam',$6,$7,$8,$9::jsonb,$10,$11,$12,$13,now())
			ON CONFLICT (source_id,uid) DO UPDATE SET
			  name=excluded.name, job_type=excluded.job_type, backup_server=excluded.backup_server,
			  is_disabled=excluded.is_disabled, last_result=excluded.last_result, last_run=excluded.last_run,
			  raw=excluded.raw, backup_server_id=excluded.backup_server_id,
			  schedule_enabled=excluded.schedule_enabled, last_result_raw=excluded.last_result_raw,
			  server_name=excluded.server_name, updated_at=now()`,
			srcID, j.UID, j.Name, jt, srv, !j.ScheduleEnabled,
			veeamResultName(j.LastResult), j.LastRun, string(raw),
			j.ServerID, j.ScheduleEnabled, j.LastResult, srv); err != nil {
			warns = append(warns, "job: "+err.Error())
			continue
		}
		nJobs++

		// Option A: one workload per job. JobSessions carries no VM identity, so
		// the job is the finest honest unit. wl_vmid is a stable synthetic id
		// (900001+) so sessions link exactly like Proxmox/ABB.
		var wlVmid *int
		if err := a.db.QueryRow(ctx, `SELECT wl_vmid FROM veeam_jobs WHERE source_id=$1 AND uid=$2`, srcID, j.UID).Scan(&wlVmid); err != nil {
			warns = append(warns, "job lookup: "+err.Error())
			continue
		}
		if wlVmid == nil {
			if err := a.db.QueryRow(ctx, `UPDATE veeam_jobs SET wl_vmid=nextval('veeam_wl_vmid_seq')
				WHERE source_id=$1 AND uid=$2 AND wl_vmid IS NULL RETURNING wl_vmid`, srcID, j.UID).Scan(&wlVmid); err != nil {
				warns = append(warns, "vmid alloc: "+err.Error())
				continue
			}
		}
		if _, err := a.db.Exec(ctx, `INSERT INTO backup_workloads(source_id,vmid,name,node,kind,status,template,last_seen,ext_id)
			VALUES($1,$2,$3,$4,'veeam_job',$5,false,now(),$6)
			ON CONFLICT (source_id,vmid) DO UPDATE SET name=excluded.name,node=excluded.node,kind=excluded.kind,status=excluded.status,last_seen=now(),ext_id=excluded.ext_id`,
			srcID, *wlVmid, j.Name, srv, veeamResultName(j.LastResult), j.UID); err != nil {
			warns = append(warns, "workload: "+err.Error())
		}
	}

	// Option B: per-VM workloads. The collector reports one row per VM (its
	// newest object-level run) because object-level runs must not be written
	// into backup_sessions — those carry the job-level KPI. Facts for these
	// rows live in veeam_vms and are folded into the workload query.
	nTasks := 0
	for _, t := range in.Tasks {
		vmName := strings.TrimSpace(t.VMName)
		if vmName == "" {
			nSkip++
			continue
		}
		key := strings.TrimSpace(t.VMUID)
		if key == "" {
			key = "name:" + strings.ToLower(vmName)
		}
		srv := serverName[t.ServerID]
		if srv == "" && t.JobName != "" {
			// Fall back to the job's node when the payload omitted the server id.
			var bs string
			if err := a.db.QueryRow(ctx, `SELECT backup_server FROM veeam_jobs WHERE source_id=$1 AND name=$2 LIMIT 1`, srcID, t.JobName).Scan(&bs); err == nil {
				srv = bs
			}
		}
		if srv == "" {
			srv = t.ServerID
		}
		srv = nodeName(srv)

		var vm int
		if err := a.db.QueryRow(ctx, `INSERT INTO veeam_vms(source_id,vm_key,vm_name,vmid,job_name,node,last_run_at,last_status,last_success,last_reason,last_size,updated_at)
			VALUES($1,$2,$3,nextval('veeam_vm_vmid_seq'),$4,$5,$6,$7,$8,$9,$10,now())
			ON CONFLICT (source_id,vm_key) DO UPDATE SET
			  vm_name=excluded.vm_name, job_name=excluded.job_name, node=excluded.node,
			  last_run_at=excluded.last_run_at, last_status=excluded.last_status,
			  -- Keep the newest known success: a failed run must not erase the
			  -- fact that the VM was backed up successfully earlier.
			  last_success=GREATEST(veeam_vms.last_success, excluded.last_success),
			  last_reason=excluded.last_reason, last_size=excluded.last_size, updated_at=now()
			RETURNING vmid`,
			srcID, key, vmName, t.JobName, srv, t.StartedAt, t.Status, t.LastSuccess,
			strings.TrimSpace(t.Reason), t.SizeBytes).Scan(&vm); err != nil {
			warns = append(warns, "vm: "+err.Error())
			continue
		}
		if _, err := a.db.Exec(ctx, `INSERT INTO backup_workloads(source_id,vmid,name,node,kind,status,template,last_seen,ext_id)
			VALUES($1,$2,$3,$4,'veeam_vm',$5,false,now(),$6)
			ON CONFLICT (source_id,vmid) DO UPDATE SET name=excluded.name,node=excluded.node,kind=excluded.kind,status=excluded.status,last_seen=now(),ext_id=excluded.ext_id`,
			srcID, vm, vmName, srv, t.Status, key); err != nil {
			warns = append(warns, "vm workload: "+err.Error())
			continue
		}
		nTasks++
	}

	// name+server -> vmid, built after the job loop so freshly seen jobs are
	// resolvable for this same payload.
	wlByKey := map[string]int{}
	if rows, err := a.db.Query(ctx, `SELECT wl_vmid, name, backup_server_id, backup_server FROM veeam_jobs
		WHERE source_id=$1 AND wl_vmid IS NOT NULL`, srcID); err == nil {
		for rows.Next() {
			var vm int
			var name, sid, srv string
			if rows.Scan(&vm, &name, &sid, &srv) == nil {
				if sid != "" {
					wlByKey[name+"\x00"+sid] = vm
				}
				if srv != "" {
					wlByKey[name+"\x00"+srv] = vm
				}
			}
		}
		rows.Close()
	}

	for _, s := range in.Sessions {
		if s.ID == "" || !isBackupJobType(s.JobType) {
			nSkip++
			continue
		}
		srv := serverName[s.ServerID]
		if srv == "" {
			srv = s.ServerID
		}
		srv = nodeName(srv)
		msg := strings.TrimSpace(s.Reason)
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
		// Link the session to the job-level workload. JobSessions gives us
		// job_name + db_instance_id only, so the key is (name, server id).
		// Unresolved sessions still land with vmid=0 so nothing is dropped.
		wlVmid := 0
		var wlID *string
		if vm, ok := wlByKey[s.JobName+"\x00"+s.ServerID]; ok && s.JobName != "" {
			wlVmid = vm
			var id string
			if err := a.db.QueryRow(ctx, `SELECT id::text FROM backup_workloads WHERE source_id=$1 AND vmid=$2`, srcID, vm).Scan(&id); err == nil {
				wlID = &id
			}
		}
		// Update-then-insert on (source_id,upid): once a session is relinked to a
		// vmid its conflict key changes, so a plain upsert on
		// (source_id,upid,vmid) would re-insert the same run every poll.
		tag, err := a.db.Exec(ctx, `UPDATE backup_sessions SET
			  vmid=$3, node=$4, started_at=$5, ended_at=$6, status=$7, message=$8,
			  size_bytes=$9, raw_status=$10, backup_server=$4, job_name=$11, workload_id=$12
			WHERE source_id=$1 AND upid=$2`,
			srcID, s.ID, wlVmid, srv, s.StartedAt, s.EndedAt,
			veeamResultName(s.Result), msg, s.SizeBytes, intToStr(s.Result), s.JobName, wlID)
		if err == nil && tag.RowsAffected() == 0 {
			_, err = a.db.Exec(ctx, `INSERT INTO backup_sessions
				(source_id,vmid,upid,node,started_at,ended_at,status,message,size_bytes,raw_status,backup_server,job_name,workload_id)
				VALUES($1,$3,$2,$4,$5,$6,$7,$8,$9,$10,$4,$11,$12)`,
				srcID, s.ID, wlVmid, srv, s.StartedAt, s.EndedAt,
				veeamResultName(s.Result), msg, s.SizeBytes, intToStr(s.Result), s.JobName, wlID)
		}
		if err != nil {
			warns = append(warns, "session: "+err.Error())
		} else {
			nSess++
		}
	}

	// Repository capacity (1.0.5+). Replace-set semantics: rows not reported
	// this run are removed. Skipped when the collector sent none (older versions).
	if len(in.Repos) > 0 {
		rstart := time.Now()
		for _, r := range in.Repos {
			nm := strings.TrimSpace(r.Name)
			if nm == "" || r.TotalBytes <= 0 {
				continue
			}
			used := r.TotalBytes - r.FreeBytes
			if used < 0 {
				used = 0
			}
			if _, err := a.db.Exec(ctx, `INSERT INTO storage_pools(source_id,node,name,kind,content,shared,total_bytes,used_bytes,online,updated_at)
				VALUES($1,$2,$3,'veeam_repo','backup',EXISTS(SELECT 1 FROM storage_shared_rules r WHERE $3 ILIKE r.name_pattern),$4,$5,true,now())
				ON CONFLICT (source_id,node,name) DO UPDATE SET total_bytes=excluded.total_bytes,used_bytes=excluded.used_bytes,shared=excluded.shared,online=true,updated_at=now()`,
				srcID, strings.TrimSpace(r.Server), nm, r.TotalBytes, used); err != nil {
				warns = append(warns, "repo: "+err.Error())
			}
		}
		a.db.Exec(ctx, `DELETE FROM storage_pools WHERE source_id=$1 AND updated_at < $2`, srcID, rstart)
	}

	status, msg := "success", ""
	if len(warns) > 0 {
		status = "partial"
		msg = strings.Join(warns, "; ")
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
	}
	counts, _ := json.Marshal(map[string]int{
		"servers": len(in.Servers), "jobs_upserted": nJobs,
		"sessions_upserted": nSess, "vms_upserted": nTasks, "skipped": nSkip,
	})
	a.db.Exec(ctx, `UPDATE backup_sources SET last_poll_at=now(),connection_status=$2,last_error=$3
		WHERE id=$1`, srcID, map[string]string{"success": "ok"}[status], msg)
	a.db.Exec(ctx, `INSERT INTO source_poll_runs(source_id,finished_at,status,error,counts)
		VALUES($1,now(),$2,$3,$4::jsonb)`, srcID, status, msg, string(counts))

	writeJSON(w, 200, map[string]any{
		"ok": true, "jobs": nJobs, "sessions": nSess, "tasks": nTasks, "skipped": nSkip,
		"warnings": warns,
	})
}

// ingestTokenIssue rotates the push token for a source and returns the new one
// exactly once. The previous token is revoked so a leaked collector file cannot
// keep writing after rotation.
func (a *App) ingestTokenIssue(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		apiErr(w, 404, "not_found", "no such source")
		return
	}
	var platform, name string
	if err := a.db.QueryRow(r.Context(), `SELECT platform,name FROM backup_sources WHERE id=$1`, id).Scan(&platform, &name); err != nil {
		apiErr(w, 404, "not_found", "no such source")
		return
	}
	if platform != "veeam_push" {
		apiErr(w, 409, "wrong_platform", "only push sources have ingest tokens")
		return
	}
	token, hash := newToken()
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		apiErr(w, 500, "db", "begin failed")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `UPDATE ingest_tokens SET revoked=true WHERE source_id=$1 AND NOT revoked`, id); err != nil {
		apiErr(w, 500, "db", "revoke failed")
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO ingest_tokens(source_id,label,token_hash) VALUES($1,$2,$3)`, id, name, hash); err != nil {
		apiErr(w, 500, "db", "insert failed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		apiErr(w, 500, "db", "commit failed")
		return
	}
	a.aud(r, u, "source.ingest_token", id, "success", map[string]any{"rotated": true})
	writeJSON(w, 200, map[string]string{"ingestToken": token})
}

func intToStr(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [12]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
