package main

import (
	"net/http"
	"strconv"
	"time"
)

const wlSQL = `SELECT * FROM (
 SELECT w.id::text AS id, w.source_id::text AS source_id, bs.name AS source_name, w.vmid, w.name, w.node, w.kind, w.status,
  ls.last_success, la.status AS last_status, la.ts AS last_attempt, coalesce(la.message,'') AS last_message,
  ab.next_run,
  CASE WHEN coalesce(vj.is_disabled, j.is_disabled, false) OR (ab.n > 0 AND ab.next_run IS NULL) THEN 'disabled'
       WHEN ls.last_success IS NULL THEN 'never'
       WHEN la.status='failed' AND la.ts > ls.last_success THEN 'failed'
       WHEN ls.last_success < now() - make_interval(hours => $1::int) THEN 'stale'
       ELSE 'ok' END AS state
 FROM backup_workloads w JOIN backup_sources bs ON bs.id=w.source_id
 -- Option B: per-VM rows carry their own run facts (one row per VM, newest
 -- object-level run), because object-level runs are deliberately not written
 -- into backup_sessions — that would inflate every session count.
 LEFT JOIN veeam_vms vv ON vv.source_id=w.source_id AND w.kind='veeam_vm' AND vv.vmid=w.vmid
 -- A Veeam job disabled in the product is not a coverage gap. Job rows look it
 -- up by uid; per-VM rows carry the job name that backs the VM.
 LEFT JOIN veeam_jobs j ON j.source_id=w.source_id AND j.uid=w.ext_id AND w.kind='veeam_job'
 LEFT JOIN veeam_jobs vj ON vj.source_id=w.source_id AND vj.name=vv.job_name AND w.kind='veeam_vm'
 -- ABB: schedule comes from the task list (next_trigger_time <= 0 means no schedule).
LEFT JOIN LATERAL (SELECT count(*) AS n,
    max(CASE WHEN (t.raw->>'next_trigger_time')::bigint > 0 THEN to_timestamp((t.raw->>'next_trigger_time')::bigint) END) AS next_run
  FROM abb_tasks t WHERE w.kind='synology_abb' AND t.source_id=w.source_id
   AND EXISTS (SELECT 1 FROM jsonb_array_elements(coalesce(t.raw->'devices','[]'::jsonb)) d WHERE (d->>'device_id')::int=w.vmid)) ab ON true
LEFT JOIN LATERAL (SELECT GREATEST(
    (SELECT max(coalesce(s.ended_at,s.started_at)) FROM backup_sessions s WHERE s.source_id=w.source_id AND s.vmid=w.vmid AND s.status IN ('success','warning')),
    (SELECT max(p.ctime) FROM backup_points p WHERE p.source_id=w.source_id AND p.vmid=w.vmid),
    vv.last_success) AS last_success) ls ON true
 LEFT JOIN LATERAL (SELECT q.status, q.ts, q.message FROM (
    SELECT s.status, coalesce(s.ended_at,s.started_at) AS ts, s.message FROM backup_sessions s
     WHERE s.source_id=w.source_id AND s.vmid=w.vmid
    UNION ALL
    SELECT vv.last_status, vv.last_run_at, vv.last_reason WHERE vv.last_run_at IS NOT NULL
   ) q ORDER BY q.ts DESC NULLS LAST LIMIT 1) la ON true
 WHERE w.status<>'removed' AND NOT w.template AND bs.enabled
 -- A job that contributed at least one per-VM row is represented by those
 -- rows; keeping the job row too would count every VM twice. A job with no VM
 -- rows (disabled, or a job type that reports no object sessions) keeps its
 -- job-level row so its inventory and its disabled state stay visible.
 AND NOT (w.kind='veeam_job' AND EXISTS (
    SELECT 1 FROM veeam_vms v2 WHERE v2.source_id=w.source_id AND v2.job_name=w.name))
) x`

type wlOut struct {
	ID          string     `json:"id"`
	SourceID    string     `json:"sourceId"`
	SourceName  string     `json:"sourceName"`
	VMID        int        `json:"vmid"`
	Name        string     `json:"name"`
	Node        string     `json:"node"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	LastSuccess *time.Time `json:"lastSuccess"`
	LastStatus  *string    `json:"lastStatus"`
	LastAttempt *time.Time `json:"lastAttempt"`
	LastMessage string     `json:"lastMessage"`
	NextRun     *time.Time `json:"nextRun"`
	State       string     `json:"state"`
}

func staleHours(r *http.Request) int {
	if v, err := strconv.Atoi(r.URL.Query().Get("staleHours")); err == nil && v >= 1 && v <= 24*365 {
		return v
	}
	return 48
}

func (a *App) workloadsList(w http.ResponseWriter, r *http.Request, u *User) {
	q := r.URL.Query()
	rows, err := a.db.Query(r.Context(), wlSQL+` WHERE ($2='' OR state=$2) AND ($3='' OR name ILIKE '%'||$3||'%' OR vmid::text=$3) AND ($4='' OR source_id=$4)
		ORDER BY CASE state WHEN 'failed' THEN 0 WHEN 'never' THEN 1 WHEN 'stale' THEN 2 WHEN 'disabled' THEN 4 ELSE 3 END, lower(name) LIMIT 2000`,
		staleHours(r), q.Get("state"), q.Get("q"), q.Get("sourceId"))
	if err != nil {
		apiErr(w, 500, "db", "query failed")
		return
	}
	defer rows.Close()
	out := []wlOut{}
	for rows.Next() {
		var o wlOut
		if rows.Scan(&o.ID, &o.SourceID, &o.SourceName, &o.VMID, &o.Name, &o.Node, &o.Kind, &o.Status, &o.LastSuccess, &o.LastStatus, &o.LastAttempt, &o.LastMessage, &o.NextRun, &o.State) == nil {
			out = append(out, o)
		}
	}
	writeJSON(w, 200, out)
}

func (a *App) sessionsList(w http.ResponseWriter, r *http.Request, u *User) {
	q := r.URL.Query()
	lim := 200
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 1000 {
		lim = v
	}
	vm := -1
	if v, err := strconv.Atoi(q.Get("vmid")); err == nil {
		vm = v
	}
	rows, err := a.db.Query(r.Context(), `SELECT s.id, bs.name, s.vmid, coalesce(w.name,''), s.node, s.started_at, s.ended_at, s.status, s.message, s.size_bytes
		FROM backup_sessions s JOIN backup_sources bs ON bs.id=s.source_id LEFT JOIN backup_workloads w ON w.id=s.workload_id
		WHERE ($1='' OR s.status=$1) AND ($2<0 OR s.vmid=$2)
		ORDER BY coalesce(s.ended_at,s.started_at) DESC LIMIT $3`, q.Get("status"), vm, lim)
	if err != nil {
		apiErr(w, 500, "db", "query failed")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var src, name, node, status, msg string
		var vmid int
		var st, en *time.Time
		var size int64
		if rows.Scan(&id, &src, &vmid, &name, &node, &st, &en, &status, &msg, &size) == nil {
			out = append(out, map[string]any{"id": id, "source": src, "vmid": vmid, "name": name, "node": node, "startedAt": st, "endedAt": en, "status": status, "message": msg, "sizeBytes": size})
		}
	}
	writeJSON(w, 200, out)
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request, u *User) {
	ctx := r.Context()
	var total, enabled int
	a.db.QueryRow(ctx, "SELECT count(*),count(*) FILTER (WHERE enabled) FROM backup_sources").Scan(&total, &enabled)
	state := "ok"
	if total == 0 {
		state = "no_sources"
	}
	var beat *time.Time
	a.db.QueryRow(ctx, "SELECT last_beat FROM service_heartbeat WHERE service='poller'").Scan(&beat)
	cov := map[string]int{"ok": 0, "stale": 0, "failed": 0, "never": 0, "disabled": 0}
	running, all := 0, 0
	if rows, err := a.db.Query(ctx, `SELECT state, count(*), count(*) FILTER (WHERE status='running') FROM (`+wlSQL+`) y GROUP BY state`, staleHours(r)); err == nil {
		for rows.Next() {
			var s string
			var c, rn int
			if rows.Scan(&s, &c, &rn) == nil {
				cov[s] = c
				all += c
				running += rn
			}
		}
		rows.Close()
	}
	sess := map[string]int{"success": 0, "warning": 0, "failed": 0}
	if rows, err := a.db.Query(ctx, `SELECT status,count(*) FROM backup_sessions WHERE coalesce(ended_at,started_at) > now() - interval '24 hours' GROUP BY status`); err == nil {
		for rows.Next() {
			var s string
			var c int
			if rows.Scan(&s, &c) == nil {
				sess[s] = c
			}
		}
		rows.Close()
	}
	// Daily report: 24h session counts per platform, 14-day ABB trend, coverage.
	// Day buckets use Asia/Jakarta so they match the daily report.
	plat := map[string]map[string]int{
		"proxmox": {"success": 0, "warning": 0, "failed": 0},
		"abb":     {"success": 0, "warning": 0, "failed": 0},
		"veeam":   {"success": 0, "warning": 0, "failed": 0},
	}
	pmap := map[string]string{"proxmox": "proxmox", "synology_abb": "abb", "veeam_push": "veeam", "veeam": "veeam"}
	if rows, err := a.db.Query(ctx, `SELECT b.platform, s.status, count(*) FROM backup_sessions s JOIN backup_sources b ON b.id=s.source_id
		WHERE b.enabled AND coalesce(s.ended_at,s.started_at) > now() - interval '24 hours' GROUP BY 1,2`); err == nil {
		for rows.Next() {
			var p, s string
			var c int
			if rows.Scan(&p, &s, &c) == nil {
				if k, ok := pmap[p]; ok {
					plat[k][s] = c
				}
			}
		}
		rows.Close()
	}
	// ABB: one result per task (latest in window), not one row per device/log line.
	// Veeam: latest result per job (veeam_jobs), not every raw run/retry.
	// Warning is folded into success for PVE/Veeam to match the daily report.
	for _, k := range []string{"abb", "veeam"} {
		plat[k] = map[string]int{"success": 0, "warning": 0, "failed": 0}
	}
	// ABB: one verdict per task, taken from the newest ABB log line in the
	// window (the same rule the 07:00 report uses). A merge-stage error
	// (class_name='abb_merge') is a warning, not a failed backup.
	if rows, err := a.db.Query(ctx, `SELECT CASE WHEN e.error_code=0 THEN 'success' WHEN e.class_name='abb_merge' THEN 'warning' ELSE 'failed' END, count(*)
		FROM (SELECT DISTINCT ON (task_id) task_id, error_code, class_name FROM abb_events
		      WHERE log_time > now() - interval '24 hours' AND task_id <> 0
		      ORDER BY task_id, log_time DESC) e GROUP BY 1`); err == nil {
		for rows.Next() {
			var s string
			var c int
			if rows.Scan(&s, &c) == nil {
				plat["abb"][s] = c
			}
		}
		rows.Close()
	}
	// Veeam: last run = the newer of the job column and the newest ingested
	// job session. The push collector fills job.last_run from EM's
	// JobSessions, but a job can have a fresher session row on our side (or a
	// stale column), so the KPI must use the latest evidence of a run.
	if rows, err := a.db.Query(ctx, `WITH run AS (
		SELECT j.id, j.last_result, GREATEST(j.last_run, s.last_sess) AS last_run
		FROM veeam_jobs j JOIN backup_sources b ON b.id=j.source_id
		LEFT JOIN LATERAL (SELECT max(coalesce(z.ended_at,z.started_at)) AS last_sess
			FROM backup_sessions z WHERE z.source_id=j.source_id AND z.job_name=j.name) s ON true
		WHERE b.enabled AND NOT j.is_disabled)
		SELECT CASE WHEN last_result IN ('failed','error') THEN 'failed' ELSE 'success' END, count(*)
		FROM run WHERE last_run > now() - interval '24 hours' GROUP BY 1`); err == nil {
		for rows.Next() {
			var s string
			var c int
			if rows.Scan(&s, &c) == nil {
				plat["veeam"][s] = c
			}
		}
		rows.Close()
	}
	plat["proxmox"]["success"] += plat["proxmox"]["warning"]
	plat["proxmox"]["warning"] = 0
	trend := []map[string]any{}
	if rows, err := a.db.Query(ctx, `SELECT to_char(d,'YYYY-MM-DD'), coalesce(x.s,0), coalesce(x.w,0), coalesce(x.f,0) FROM
		generate_series((now() AT TIME ZONE 'Asia/Jakarta')::date - 13, (now() AT TIME ZONE 'Asia/Jakarta')::date, interval '1 day') d
		LEFT JOIN (SELECT dd, count(*) FILTER (WHERE error_code=0) s, count(*) FILTER (WHERE error_code<>0 AND class_name='abb_merge') w, count(*) FILTER (WHERE error_code<>0 AND class_name<>'abb_merge') f
			FROM (SELECT DISTINCT ON ((e.log_time AT TIME ZONE 'Asia/Jakarta')::date, e.task_id)
				(e.log_time AT TIME ZONE 'Asia/Jakarta')::date AS dd, e.error_code, e.class_name
				FROM abb_events e JOIN backup_sources b ON b.id=e.source_id WHERE b.platform='synology_abb' AND e.task_id <> 0
				ORDER BY (e.log_time AT TIME ZONE 'Asia/Jakarta')::date, e.task_id, e.log_time DESC) q
			GROUP BY dd) x ON x.dd=d::date ORDER BY d`); err == nil {
		for rows.Next() {
			var day string
			var s, w, f int
			if rows.Scan(&day, &s, &w, &f) == nil {
				trend = append(trend, map[string]any{"day": day, "success": s, "warning": w, "failed": f})
			}
		}
		rows.Close()
	}
	var pveTotal, pveCovered int
	a.db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE EXISTS (SELECT 1 FROM backup_sessions s WHERE s.workload_id=w.id))
		FROM backup_workloads w JOIN backup_sources b ON b.id=w.source_id WHERE b.platform='proxmox' AND b.enabled AND NOT w.template AND w.status<>'removed'`).Scan(&pveTotal, &pveCovered)
	var veeamMissed int
	a.db.QueryRow(ctx, `SELECT count(*) FROM (
		SELECT GREATEST(j.last_run, s.last_sess) AS last_run FROM veeam_jobs j JOIN backup_sources b ON b.id=j.source_id
		LEFT JOIN LATERAL (SELECT max(coalesce(z.ended_at,z.started_at)) AS last_sess
			FROM backup_sessions z WHERE z.source_id=j.source_id AND z.job_name=j.name) s ON true
		WHERE b.enabled AND NOT j.is_disabled AND j.schedule_enabled) r
		WHERE r.last_run IS NULL OR r.last_run < now() - interval '24 hours'`).Scan(&veeamMissed)
	daily := map[string]any{
		"platforms": plat, "abbTrend": trend,
		"pve": map[string]int{"total": pveTotal, "covered": pveCovered, "missing": pveTotal - pveCovered},
		"veeamNotRun": veeamMissed,
	}
	srcs := []map[string]any{}
	if rows, err := a.db.Query(ctx, `SELECT id::text,name,platform,enabled,connection_status,last_poll_at,last_error FROM backup_sources ORDER BY lower(name)`); err == nil {
		for rows.Next() {
			var id, name, pf, cs, le string
			var en bool
			var lp *time.Time
			if rows.Scan(&id, &name, &pf, &en, &cs, &lp, &le) == nil {
				srcs = append(srcs, map[string]any{"id": id, "name": name, "platform": pf, "enabled": en, "connectionStatus": cs, "lastPollAt": lp, "lastError": le})
			}
		}
		rows.Close()
	}
	var alertsOpen, alertsCrit int
	a.db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='open'), count(*) FILTER (WHERE state='open' AND severity='critical') FROM alerts WHERE state<>'resolved'`).Scan(&alertsOpen, &alertsCrit)
	var polledAny int
	a.db.QueryRow(ctx, "SELECT count(*) FROM backup_sources WHERE last_poll_at IS NOT NULL").Scan(&polledAny)
	if total > 0 && polledAny == 0 {
		state = "awaiting_first_poll"
	}
	writeJSON(w, 200, map[string]any{
		"dataState": state, "sourcesTotal": total, "sourcesEnabled": enabled,
		"pollerLastBeat": beat, "generatedAt": time.Now().UTC(),
		"workloads": map[string]any{"total": all, "running": running, "covered": all - cov["never"] - cov["disabled"], "ok": cov["ok"], "stale": cov["stale"], "failed": cov["failed"], "never": cov["never"], "disabled": cov["disabled"]},
		"daily": daily, "alerts": map[string]int{"open": alertsOpen, "critical": alertsCrit}, "sessions24h": sess, "sources": srcs, "staleHours": staleHours(r),
	})
}
