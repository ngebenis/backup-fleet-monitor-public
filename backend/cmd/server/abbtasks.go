package main

import (
	"net/http"
	"time"
)

type abbTaskOut struct {
	SourceName string     `json:"sourceName"`
	TaskID     int64      `json:"taskId"`
	Name       string     `json:"name"`
	Devices    int        `json:"devices"`
	NextRun    *time.Time `json:"nextRun"`
	Schedule   string     `json:"schedule"`
	LastEnd    *time.Time `json:"lastEnd"`
	LastStatus *int       `json:"lastStatus"`
	Codes24h   string     `json:"codes24h"`
}

// abbTasksList: one row per ABB task (read from abb_tasks), with schedule and
// the latest result/error codes. next_trigger_time <= 0 means no schedule.
func (a *App) abbTasksList(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(r.Context(), `SELECT bs.name, t.task_id, t.task_name,
  jsonb_array_length(coalesce(t.raw->'devices','[]'::jsonb)),
  CASE WHEN (t.raw->>'next_trigger_time')::bigint > 0 THEN to_timestamp((t.raw->>'next_trigger_time')::bigint) END,
  coalesce(t.raw->'sched_content'->>'repeat_type',''),
  lr.ended_at, lr.status,
  coalesce((SELECT string_agg(DISTINCT e.error_code::text, ',') FROM abb_events e
     WHERE e.source_id=t.source_id AND e.task_id=t.task_id AND e.error_code IN (3207,3093) AND e.log_time > now()-interval '24 hours'),'')
FROM abb_tasks t JOIN backup_sources bs ON bs.id=t.source_id AND bs.enabled
LEFT JOIN LATERAL (SELECT x.ended_at, x.status FROM abb_results x WHERE x.source_id=t.source_id AND x.task_id=t.task_id
   ORDER BY coalesce(x.ended_at,x.started_at) DESC NULLS LAST LIMIT 1) lr ON true
ORDER BY (CASE WHEN (t.raw->>'next_trigger_time')::bigint > 0 THEN 0 ELSE 1 END), lower(t.task_name)`)
	if err != nil {
		apiErr(w, 500, "db", "query failed")
		return
	}
	defer rows.Close()
	out := []abbTaskOut{}
	for rows.Next() {
		var o abbTaskOut
		if rows.Scan(&o.SourceName, &o.TaskID, &o.Name, &o.Devices, &o.NextRun, &o.Schedule, &o.LastEnd, &o.LastStatus, &o.Codes24h) == nil {
			out = append(out, o)
		}
	}
	writeJSON(w, 200, out)
}
