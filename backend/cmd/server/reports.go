package main

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"time"
)

var reportQueries = map[string]struct {
	cols []string
	sql  string
}{
	"pve_missing": {[]string{"vmid", "name", "node", "kind"},
		`SELECT w.vmid::text, w.name, w.node, w.kind FROM backup_workloads w JOIN backup_sources b ON b.id=w.source_id
		 WHERE b.platform='proxmox' AND b.enabled AND NOT w.template AND w.status<>'removed'
		 AND NOT EXISTS (SELECT 1 FROM backup_sessions s WHERE s.workload_id=w.id) ORDER BY w.vmid`},
	"veeam_notrun": {[]string{"job", "server", "type", "last_run", "last_result"},
		`SELECT j.name, coalesce(j.server_name,''), j.job_type, coalesce(to_char(r.last_run AT TIME ZONE 'Asia/Jakarta','YYYY-MM-DD HH24:MI'),'> 48h (di luar jendela)'), coalesce(j.last_result,'')
		 FROM veeam_jobs j JOIN backup_sources b ON b.id=j.source_id
		 LEFT JOIN LATERAL (SELECT GREATEST(j.last_run, max(coalesce(z.ended_at,z.started_at))) AS last_run FROM backup_sessions z WHERE z.source_id=j.source_id AND z.job_name=j.name) r ON true
		 WHERE b.enabled AND NOT j.is_disabled AND j.schedule_enabled AND (r.last_run IS NULL OR r.last_run < now() - interval '24 hours') ORDER BY r.last_run NULLS FIRST, j.name`},
	"abb_merge": {[]string{"time", "task", "device", "error_code"},
		`SELECT to_char(log_time AT TIME ZONE 'Asia/Jakarta','YYYY-MM-DD HH24:MI'), task_name, device_name, error_code::text FROM abb_events
		 WHERE log_time > now() - interval '24 hours' AND error_code<>0 AND class_name='abb_merge' ORDER BY log_time DESC`},
	"abb_failed": {[]string{"time", "task", "device", "error_code"},
		`SELECT to_char(log_time AT TIME ZONE 'Asia/Jakarta','YYYY-MM-DD HH24:MI'), task_name, device_name, error_code::text FROM abb_events
		 WHERE log_time > now() - interval '24 hours' AND error_code<>0 AND class_name<>'abb_merge' ORDER BY log_time DESC`},
	"storage": {[]string{"storage", "node", "type", "used_gb", "total_gb", "used_pct"},
		`SELECT p.name, CASE WHEN p.shared THEN 'shared' ELSE p.node END, p.kind, round(p.used_bytes/1e9)::text, round(p.total_bytes/1e9)::text,
		 round(p.used_bytes*100.0/p.total_bytes,1)::text FROM storage_pools p JOIN backup_sources b ON b.id=p.source_id AND b.enabled
		 WHERE p.total_bytes>0 ORDER BY p.used_bytes::float8/p.total_bytes DESC`},
}

// reportData returns tabular rows (JSON) or a CSV download (?format=csv). Read-only.
func (a *App) reportData(w http.ResponseWriter, r *http.Request, u *User) {
	typ := r.URL.Query().Get("type")
	q, ok := reportQueries[typ]
	if !ok {
		apiErr(w, 400, "bad_type", "unknown report type")
		return
	}
	rows, err := a.db.Query(r.Context(), q.sql)
	if err != nil {
		apiErr(w, 500, "db", "query failed")
		return
	}
	defer rows.Close()
	out := [][]string{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			continue
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			if v != nil {
				row[i] = fmt.Sprint(v)
			}
		}
		out = append(out, row)
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.csv"`, typ, time.Now().Format("20060102")))
		cw := csv.NewWriter(w)
		cw.Write(q.cols)
		for _, row := range out {
			for i, c := range row { // neutralise spreadsheet formula injection
				if len(c) > 0 && (c[0] == '=' || c[0] == '+' || c[0] == '-' || c[0] == '@' || c[0] == '\t' || c[0] == '\r') {
					row[i] = "'" + c
				}
			}
			cw.Write(row)
		}
		cw.Flush()
		return
	}
	writeJSON(w, 200, map[string]any{"type": typ, "columns": q.cols, "rows": out})
}
