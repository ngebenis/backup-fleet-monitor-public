package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Veeam Backup Enterprise Manager REST v1.5 (XML). Read-only: logon, GET, logoff of our own session.
// Never calls job control or restore endpoints.

type veeamRef struct {
	Type string `xml:"Type,attr"`
	Href string `xml:"Href,attr"`
	Name string `xml:"Name,attr"`
	UID  string `xml:"UID,attr"`
}

type veeamTaskSession struct {
	UID           string `xml:"UID"`
	Name          string `xml:"Name"`
	JobSessionUID string `xml:"JobSessionUid"`
	JobUID        string `xml:"JobUid"`
	CreationTime  string `xml:"CreationTime"`
	EndTime       string `xml:"EndTime"`
	State         string `xml:"State"`
	Result        string `xml:"Result"`
	Reason        string `xml:"Reason"`
	TotalSize     int64  `xml:"TotalSize"`
	VmUID         string `xml:"VmUid"`
	VmName        string `xml:"VmDisplayName"`
}

type veeamJobEntity struct {
	UID             string `xml:"UID"`
	Name            string `xml:"Name"`
	JobType         string `xml:"JobType"`
	Platform        string `xml:"Platform"`
	IsDisabled      bool   `xml:"IsDisabled"`
	LatestResult    string `xml:"LatestResult"`
	LastRunTime     string `xml:"LastRunTime"`
	BackupServerUID string `xml:"BackupServerUid"`
}

// veeamGetXML GETs a resource with a bounded body and unmarshals XML into out.
func veeamGetXML(ctx context.Context, cl *pveClient, sid, path string, out any) error {
	b, code, err := veeamRaw(ctx, cl, sid, path)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("GET %s: HTTP %d", strings.SplitN(path, "?", 2)[0], code)
	}
	return xml.Unmarshal(b, out)
}

func veeamTS(t string) *time.Time {
	t = strings.TrimSpace(t)
	if t == "" {
		return nil
	}
	for _, l := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999Z07:00"} {
		if v, err := time.Parse(l, t); err == nil {
			u := v.UTC()
			return &u
		}
	}
	return nil
}

func veeamStatus(result, state string) string {
	switch strings.ToLower(strings.TrimSpace(result)) {
	case "success":
		return "success"
	case "warning":
		return "warning"
	case "failed":
		return "failed"
	}
	if strings.EqualFold(state, "InProgress") || strings.EqualFold(state, "Pending") || strings.EqualFold(state, "Working") {
		return "warning"
	}
	return "warning"
}

// stableID derives a deterministic positive int from a Veeam UID so workloads keep a stable vmid.
func stableID(uid string) int {
	var h uint32 = 2166136261
	for i := 0; i < len(uid); i++ {
		h ^= uint32(uid[i])
		h *= 16777619
	}
	return int(h%900000000) + 100000000
}

func pollVeeam(ctx context.Context, db *pgxpool.Pool, s srcRow) (map[string]any, []string, error) {
	cl, err := s.client()
	if err != nil {
		return nil, nil, err
	}
	sid, err := veeamSession(ctx, cl)
	if err != nil {
		return nil, nil, err
	}
	defer veeamLogoff(context.Background(), cl, sid)

	counts := map[string]any{}
	var warns []string

	// 1. backup servers
	serverName := map[string]string{}
	var srvCol struct {
		Items []struct {
			UID  string `xml:"UID"`
			Name string `xml:"Name"`
		} `xml:"BackupServer"`
	}
	if err := veeamGetXML(ctx, cl, sid, "/api/backupServers?format=Entity", &srvCol); err == nil {
		for _, it := range srvCol.Items {
			serverName[it.UID] = it.Name
		}
	}
	counts["backup_servers"] = len(srvCol.Items)

	// 2. jobs
	var jobCol struct {
		Items []veeamJobEntity `xml:"Job"`
	}
	if err := veeamGetXML(ctx, cl, sid, "/api/jobs?format=Entity", &jobCol); err != nil {
		warns = append(warns, "jobs: "+err.Error())
	}
	nJobs, nDisabled := 0, 0
	for _, j := range jobCol.Items {
		if j.UID == "" {
			continue
		}
		lastRun := veeamTS(j.LastRunTime)
		if _, err := db.Exec(ctx, `INSERT INTO veeam_jobs(source_id,uid,name,job_type,backup_server,platform,is_disabled,last_result,last_run,raw)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb)
			ON CONFLICT (source_id,uid) DO UPDATE SET name=excluded.name,job_type=excluded.job_type,backup_server=excluded.backup_server,
				platform=excluded.platform,is_disabled=excluded.is_disabled,last_result=excluded.last_result,last_run=excluded.last_run,raw=excluded.raw,updated_at=now()`,
			s.ID, j.UID, j.Name, j.JobType, serverName[j.BackupServerUID], j.Platform, j.IsDisabled, j.LatestResult, lastRun, mustJSON(j)); err != nil {
			warns = append(warns, "db job: "+err.Error())
			break
		}
		nJobs++
		if j.IsDisabled {
			nDisabled++
		}
	}
	counts["jobs"] = nJobs
	counts["jobs_disabled"] = nDisabled

	// 3. backup task sessions (per-VM), newest first, bounded pages
	nSess, pages := 0, 0
	seen := map[string]bool{}
	for page := 1; page <= 6; page++ {
		p := fmt.Sprintf("/api/query?type=BackupTaskSession&format=Entity&sortDesc=CreationTime&pageSize=500&page=%d", page)
		var col struct {
			Items []veeamTaskSession `xml:"BackupTaskSession"`
		}
		if err := veeamGetXML(ctx, cl, sid, p, &col); err != nil {
			warns = append(warns, "taskSessions: "+err.Error())
			break
		}
		pages++
		for _, t := range col.Items {
			if t.UID == "" || seen[t.UID] {
				continue
			}
			seen[t.UID] = true
			vmName := strings.TrimSpace(t.VmName)
			vmUID := strings.TrimSpace(t.VmUID)
			if vmUID == "" {
				vmUID = "name:" + strings.ToLower(vmName)
			}
			if vmName == "" && vmUID == "" {
				continue
			}
			vmid := stableID(strings.ToLower(vmUID))
			status := veeamStatus(t.Result, t.State)
			started, ended := veeamTS(t.CreationTime), veeamTS(t.EndTime)
			var wid *string
			var id string
			if err := db.QueryRow(ctx, `INSERT INTO backup_workloads(source_id,vmid,name,node,kind,status,template,last_seen,ext_id)
				VALUES($1,$2,$3,'',$4,'',$5,now(),$6)
				ON CONFLICT (source_id,vmid) DO UPDATE SET name=excluded.name,kind=excluded.kind,last_seen=excluded.last_seen,ext_id=excluded.ext_id
				RETURNING id::text`,
				s.ID, vmid, vmName, "veeam", status, "veeam:"+vmUID).Scan(&id); err != nil {
				warns = append(warns, "db workload: "+err.Error())
				continue
			}
			wid = &id
			msg, _ := mustJSONMsg(map[string]any{"job": t.JobUID, "state": t.State, "result": t.Result, "reason": strings.TrimSpace(t.Reason)})
			if _, err := db.Exec(ctx, `INSERT INTO backup_sessions(source_id,workload_id,vmid,upid,node,started_at,ended_at,status,message,size_bytes,raw_status)
				VALUES($1,$2,$3,$4,'',$5,$6,$7,$8,$9,$10)
				ON CONFLICT (source_id,upid,vmid) DO UPDATE SET status=excluded.status,ended_at=excluded.ended_at,message=excluded.message,size_bytes=excluded.size_bytes,raw_status=excluded.raw_status`,
				s.ID, wid, vmid, t.UID, started, ended, status, msg, t.TotalSize, strings.TrimSpace(t.State+"/"+t.Result)); err != nil {
				warns = append(warns, "db session: "+err.Error())
				break
			}
			nSess++
		}
		if len(col.Items) < 500 {
			break
		}
	}
	counts["task_sessions"] = nSess
	counts["pages"] = pages
	if nSess == 0 {
		warns = append(warns, "no backup task sessions returned (EM keeps 30 days; check account role)")
	}
	return counts, warns, nil
}

func mustJSONMsg(v any) (string, error) {
	return mustJSON(v), nil
}
