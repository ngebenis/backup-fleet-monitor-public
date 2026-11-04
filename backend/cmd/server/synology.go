package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Synology DSM Web API + Active Backup for Business. Read-only: login, query/list calls, logout.
func synoCall(ctx context.Context, cl *pveClient, path string, form url.Values) (map[string]any, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", cl.base+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := cl.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("non-JSON reply HTTP %d", resp.StatusCode)
	}
	return m, nil
}

func synoLogin(ctx context.Context, cl *pveClient) (string, error) {
	m, err := synoCall(ctx, cl, "/webapi/entry.cgi", url.Values{"api": {"SYNO.API.Auth"}, "version": {"6"}, "method": {"login"},
		"account": {cl.creds.Username}, "passwd": {cl.creds.Password}, "session": {"BackupMonitor"}, "format": {"sid"}})
	if err != nil {
		return "", err
	}
	if ok, _ := m["success"].(bool); !ok {
		code := 0
		if e, ok := m["error"].(map[string]any); ok {
			if c, ok := e["code"].(float64); ok {
				code = int(c)
			}
		}
		switch code {
		case 403, 404:
			return "", errors.New("synology login failed: 2-step verification required or code invalid (use an account without 2FA for monitoring)")
		case 400:
			return "", errors.New("synology login failed: wrong account or password")
		}
		return "", fmt.Errorf("synology login failed: error code %d", code)
	}
	d, _ := m["data"].(map[string]any)
	sid, _ := d["sid"].(string)
	if sid == "" {
		return "", errors.New("synology login: no sid")
	}
	return sid, nil
}

func synoLogout(cl *pveClient, sid string) {
	synoCall(context.Background(), cl, "/webapi/entry.cgi", url.Values{"api": {"SYNO.API.Auth"}, "version": {"6"}, "method": {"logout"}, "session": {"BackupMonitor"}, "_sid": {sid}})
}

func testSynology(ctx context.Context, s srcRow) (map[string]any, error) {
	cl, err := s.client()
	if err != nil {
		return nil, err
	}
	sid, err := synoLogin(ctx, cl)
	if err != nil {
		return nil, err
	}
	defer synoLogout(cl, sid)
	info := map[string]any{"login": "ok"}
	if m, err := synoCall(ctx, cl, "/webapi/entry.cgi", url.Values{"api": {"SYNO.ActiveBackup.Version"}, "version": {"1"}, "method": {"get"}, "_sid": {sid}}); err == nil {
		if ok, _ := m["success"].(bool); ok {
			info["abbVersion"] = m["data"]
		} else {
			info["abbVersion"] = "no permission or unsupported"
		}
	}
	return info, nil
}

// abbStatus maps ABB result status codes observed on a Synology NAS: 2 = ok, 4 = failed, 5 = interrupted.
func abbStatus(code float64) (string, string) {
	switch int(code) {
	case 2:
		return "success", "OK"
	case 4:
		return "failed", "FAILED"
	case 5:
		return "warning", "INTERRUPTED"
	default:
		return "warning", fmt.Sprintf("STATUS_%d", int(code))
	}
}

// abbClass pulls other_params.error_params.string_class_name from an ABB log
// row. The daily report keys "abb_merge" failures off this field, so a merge
// error can be told apart from a real backup failure. Empty when absent.
func abbClass(l map[string]any) string {
	ep := jmap(jmap(l, "other_params"), "error_params")
	return jstr(ep, "string_class_name")
}

// abbErrDevice is the failing component reported by ABB (for example the
// repository/datastore name when a merge step fails).
func abbErrDevice(l map[string]any) string {
	ep := jmap(jmap(l, "other_params"), "error_params")
	if d := jstr(ep, "device"); d != "" {
		return d
	}
	return jstr(ep, "device_name")
}

func abbCategory(code int) string {
	switch code {
	case 3207, 3204, 3272, 3087, 3093, 3056:
		return "PROCESSING_ISSUE"
	}
	return ""
}

// synoList pulls the newest `want` rows. ABB returns newest first (DESC) and honours offset/limit.
func synoList(ctx context.Context, cl *pveClient, sid, api, method, listKey string, want int) ([]map[string]any, int, error) {
	m, err := synoCall(ctx, cl, "/webapi/entry.cgi", url.Values{"api": {api}, "version": {"1"}, "method": {method}, "offset": {"0"}, "limit": {"1"}, "_sid": {sid}})
	if err != nil {
		return nil, 0, err
	}
	d := jmap(m, "data")
	total := int(jnum(d, "count"))
	if total == 0 {
		total = int(jnum(d, "total"))
	}
	var out []map[string]any
	for off := 0; off < total && off < want; off += 500 {
		lim := 500
		if want-off < lim {
			lim = want - off
		}
		mm, err := synoCall(ctx, cl, "/webapi/entry.cgi", url.Values{"api": {api}, "version": {"1"}, "method": {method},
			"offset": {fmt.Sprint(off)}, "limit": {fmt.Sprint(lim)}, "_sid": {sid}})
		if err != nil {
			return out, total, err
		}
		list, _ := jmap(mm, "data")[listKey].([]any)
		for _, x := range list {
			if r, ok := x.(map[string]any); ok {
				out = append(out, r)
			}
		}
		if len(list) == 0 {
			break
		}
	}
	return out, total, nil
}

// pollSynology collects tasks, devices, run results and logs. Read-only toward the NAS.
func pollSynology(ctx context.Context, db *pgxpool.Pool, s srcRow) (map[string]any, []string, error) {
	cl, err := s.client()
	if err != nil {
		return nil, nil, err
	}
	sid, err := synoLogin(ctx, cl)
	if err != nil {
		return nil, nil, err
	}
	defer synoLogout(cl, sid)
	var warns []string
	counts := map[string]any{}
	now := time.Now().UTC()
	// ABB reports per-device data only, no node, so the host comes from the
	// source URL and passes through the alias table (short name -> FQDN).
	abbNode := sourceHostOf(s.BaseURL)

	if m, err := synoCall(ctx, cl, "/webapi/entry.cgi", url.Values{"api": {"SYNO.ActiveBackup.Version"}, "version": {"1"}, "method": {"get"}, "_sid": {sid}}); err == nil {
		if ok, _ := m["success"].(bool); ok {
			counts["abb_version"] = m["data"]
		}
	}

	devs, _, err := synoList(ctx, cl, sid, "SYNO.ActiveBackup.Device", "list", "devices", 4000)
	if err != nil {
		warns = append(warns, "devices: "+err.Error())
	}
	devName := map[int]string{}
	for _, d := range devs {
		if id := int(jnum(d, "device_id")); id != 0 {
			devName[id] = jstr(d, "host_name")
		}
	}
	counts["devices"] = len(devName)

	if m, err := synoCall(ctx, cl, "/webapi/entry.cgi", url.Values{"api": {"SYNO.ActiveBackup.Task"}, "version": {"1"}, "method": {"list"}, "_sid": {sid}}); err == nil {
		if ts, ok := jmap(m, "data")["tasks"].([]any); ok {
			counts["tasks"] = len(ts)
			nT := 0
			for _, x := range ts {
				tm, _ := x.(map[string]any)
				tid := int64(jnum(tm, "task_id"))
				if tid == 0 {
					continue
				}
				if _, err := db.Exec(ctx, `INSERT INTO abb_tasks(source_id,task_id,task_name,raw,updated_at) VALUES($1,$2,$3,$4::jsonb,now())
					ON CONFLICT (source_id,task_id) DO UPDATE SET task_name=excluded.task_name,raw=excluded.raw,updated_at=now()`,
					s.ID, tid, jstr(tm, "task_name"), mustJSON(synoRedact(tm))); err != nil {
					warns = append(warns, "db task: "+err.Error())
				} else {
					nT++
				}
			}
			counts["tasks_stored"] = nT
		}
	} else {
		warns = append(warns, "tasks: "+err.Error())
	}

	res, totalRes, err := synoList(ctx, cl, sid, "SYNO.ActiveBackup.Log", "list_result", "results", 3000)
	if err != nil {
		warns = append(warns, "results: "+err.Error())
	}
	counts["results_total"] = totalRes

	nSess, nOther := 0, 0
	for _, r := range res {
		status, raw := abbStatus(jnum(r, "status"))
		rid := int64(jnum(r, "result_id"))
		started := time.Unix(int64(jnum(r, "time_start")), 0).UTC()
		ended := time.Unix(int64(jnum(r, "time_end")), 0).UTC()
		ec, wc := int(jnum(r, "error_count")), int(jnum(r, "warning_count"))
		if ec > 0 && status == "success" {
			status, raw = "warning", "PARTIAL"
		}
		action := int(jnum(r, "job_action"))
		// A failed result whose only error is an abb_merge step is a warning, not a failed backup
		// (same rule as the dashboard KPI): decided later from abb_events by mergeDowngrade().
		msg, _ := json.Marshal(map[string]any{"task": jstr(r, "task_name"), "errors": ec, "warnings": wc, "action": action})
		if action != 1 {
			// restore / verify / other: tracked separately, never counted as a backup KPI
			if _, err := db.Exec(ctx, `INSERT INTO abb_results(source_id,result_id,task_id,task_name,job_action,status,error_count,warning_count,started_at,ended_at,raw)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb)
				ON CONFLICT (source_id,result_id) DO UPDATE SET status=excluded.status,error_count=excluded.error_count,warning_count=excluded.warning_count,ended_at=excluded.ended_at,raw=excluded.raw`,
				s.ID, rid, int(jnum(r, "task_id")), jstr(r, "task_name"), action, int(jnum(r, "status")), ec, wc, started, ended, mustJSON(synoRedact(r))); err != nil {
				warns = append(warns, "db result: "+err.Error())
			} else {
				nOther++
			}
			continue
		}
		dl, _ := jmap(r, "task_config")["device_list"].([]any)
		if len(dl) == 0 {
			dl = []any{map[string]any{}}
		}
		for _, x := range dl {
			dm, _ := x.(map[string]any)
			did := int(jnum(dm, "device_id"))
			if did == 0 {
				did = int(jnum(dm, "vm_id"))
			}
			if did == 0 {
				continue // no device reference: cannot attach the run to a workload
			}
			name := jstr(dm, "host_name")
			if name == "" {
				name = devName[did]
			}
			if _, err := db.Exec(ctx, `INSERT INTO backup_workloads(source_id,vmid,name,node,kind,status,template,last_seen,ext_id)
				VALUES($1,$2,$3,$4,$5,$6,false,$7,$8)
				ON CONFLICT (source_id,vmid) DO UPDATE SET name=excluded.name,node=excluded.node,kind=excluded.kind,last_seen=excluded.last_seen,ext_id=excluded.ext_id`,
				s.ID, did, name, abbNode, "synology_abb", status, now, fmt.Sprintf("abb-dev-%d", did)); err != nil {
				warns = append(warns, "db workload: "+err.Error())
				continue
			}
			upid := fmt.Sprintf("abb-%d-%d", rid, did)
			if _, err := db.Exec(ctx, `INSERT INTO backup_sessions(source_id,workload_id,vmid,upid,node,started_at,ended_at,status,message,size_bytes,raw_status)
				VALUES($1,(SELECT id FROM backup_workloads WHERE source_id=$1 AND vmid=$2),$2,$3,$4,$5,$6,$7,$8,0,$9)
				ON CONFLICT (source_id,upid,vmid) DO UPDATE SET status=excluded.status,message=excluded.message,started_at=excluded.started_at,ended_at=excluded.ended_at,raw_status=excluded.raw_status,node=excluded.node`,
				s.ID, did, upid, abbNode, started, ended, status, string(msg), raw); err != nil {
				warns = append(warns, "db session: "+err.Error())
			} else {
				nSess++
			}
		}
	}
	counts["sessions_upserted"] = nSess
	counts["non_backup_runs"] = nOther

	logs, totalLogs, err := synoList(ctx, cl, sid, "SYNO.ActiveBackup.Log", "list_log", "logs", 3000)
	if err != nil {
		warns = append(warns, "logs: "+err.Error())
	}
	counts["logs_total"] = totalLogs
	nEv := 0
	sevCount := map[string]int{}
	for _, l := range logs {
		lid := int64(jnum(l, "log_id"))
		level, code := int(jnum(l, "log_level")), int(jnum(l, "error_code"))
		sev := "info"
		switch {
		case code != 0:
			sev = "error"
		case level == 0 || level == 1:
			sev = "warning"
		}
		sevCount[sev]++
		if _, err := db.Exec(ctx, `INSERT INTO abb_events(source_id,log_id,log_time,log_level,log_type,error_code,device_id,device_name,task_id,task_name,user_name,severity,category,class_name,device,raw_log)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::jsonb)
			ON CONFLICT (source_id,log_id) DO UPDATE SET class_name=excluded.class_name, device=excluded.device, raw_log=excluded.raw_log`,
			s.ID, lid, time.Unix(int64(jnum(l, "log_time")), 0).UTC(), level, int(jnum(l, "log_type")), code,
			int(jnum(l, "device_id")), jstr(l, "device_name"), int(jnum(l, "task_id")), jstr(l, "task_name"), jstr(l, "user_name"),
			sev, abbCategory(code), abbClass(l), abbErrDevice(l), mustJSON(synoRedact(l))); err == nil {
			nEv++
		}
	}
	counts["events_inserted"] = nEv
	counts["event_severity"] = sevCount

	// Downgrade failed ABB sessions whose error is an abb_merge event (matched by task name, +-120s).
	if _, err := db.Exec(ctx, `UPDATE backup_sessions s SET status='warning', raw_status='MERGE'
		WHERE s.source_id=$1 AND s.status='failed' AND EXISTS (
		  SELECT 1 FROM abb_events e WHERE e.source_id=s.source_id AND e.class_name='abb_merge' AND e.error_code<>0
		   AND e.task_name=(s.message::jsonb->>'task')
		   AND abs(extract(epoch FROM e.log_time - coalesce(s.ended_at,s.started_at)))<=120)`, s.ID); err != nil {
		warns = append(warns, "merge downgrade: "+err.Error())
	}
	// workload state = newest run per workload
	if _, err := db.Exec(ctx, `UPDATE backup_workloads w SET status = x.status
		FROM (SELECT DISTINCT ON (s.workload_id) s.workload_id, s.status
		      FROM backup_sessions s WHERE s.source_id=$1 AND s.workload_id IS NOT NULL
		      ORDER BY s.workload_id, coalesce(s.ended_at,s.started_at) DESC) x
		WHERE w.id = x.workload_id`, s.ID); err != nil {
		warns = append(warns, "workload status: "+err.Error())
	}
	return counts, warns, nil
}
