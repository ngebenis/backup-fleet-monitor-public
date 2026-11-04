package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Alerting. The poller evaluates the current backup state every few minutes and
// reconciles it with the alerts table:
//   - a condition seen for the first time opens an alert and notifies,
//   - a condition that persists keeps its alert (and re-notifies every
//     RepeatHours while it is still unacknowledged),
//   - a condition that disappears resolves its alert (optional recovery notice).
// Acknowledging an alert silences further notifications for it.

type alertSettings struct {
	Enabled        bool `json:"enabled"`
	StaleHours     int  `json:"staleHours"`     // workload with no success for this long -> backup_stale
	IncludeNever   bool `json:"includeNever"`   // also alert on workloads that never had a success
	StoragePct     int  `json:"storagePct"`     // storage pool usage >= this -> storage_high (>=95 is critical)
	RepeatHours    int  `json:"repeatHours"`    // re-notify unacknowledged alerts every N hours (0 = never)
	NotifyRecovery bool `json:"notifyRecovery"` // send a notice when an alert resolves
}

func defaultAlertSettings() alertSettings {
	return alertSettings{Enabled: true, StaleHours: 48, IncludeNever: false, StoragePct: 85, RepeatHours: 24, NotifyRecovery: true}
}

func (s alertSettings) validate() string {
	switch {
	case s.StaleHours < 1 || s.StaleHours > 24*365:
		return "staleHours must be 1..8760"
	case s.StoragePct < 50 || s.StoragePct > 99:
		return "storagePct must be 50..99"
	case s.RepeatHours < 0 || s.RepeatHours > 24*30:
		return "repeatHours must be 0..720"
	}
	return ""
}

func loadSetting(ctx context.Context, db *pgxpool.Pool, key string, into any) {
	var raw []byte
	if err := db.QueryRow(ctx, `SELECT value FROM app_settings WHERE key=$1`, key).Scan(&raw); err == nil {
		json.Unmarshal(raw, into) // fields absent from storage keep their defaults
	}
}

func saveSetting(ctx context.Context, db *pgxpool.Pool, key string, v any) error {
	b, _ := json.Marshal(v)
	_, err := db.Exec(ctx, `INSERT INTO app_settings(key,value) VALUES($1,$2::jsonb)
		ON CONFLICT (key) DO UPDATE SET value=excluded.value, updated_at=now()`, key, string(b))
	return err
}

func loadAlertSettings(ctx context.Context, db *pgxpool.Pool) alertSettings {
	s := defaultAlertSettings()
	loadSetting(ctx, db, "alerts", &s)
	return s
}

// ---- pure reconciliation logic (unit tested) ----

type activeAlert struct {
	Fingerprint string
	Type        string
	Severity    string
	SourceID    string
	Title       string
	Detail      string
}

type existingAlert struct {
	ID          int64
	Fingerprint string
	State       string // open | acknowledged
}

type alertPlan struct {
	Insert  []activeAlert
	Refresh map[int64]activeAlert // existing id -> latest data
	Resolve []int64
}

func planAlerts(existing []existingAlert, active []activeAlert) alertPlan {
	p := alertPlan{Refresh: map[int64]activeAlert{}}
	byFP := map[string]existingAlert{}
	for _, e := range existing {
		byFP[e.Fingerprint] = e
	}
	seen := map[string]bool{}
	for _, a := range active {
		if seen[a.Fingerprint] {
			continue
		}
		seen[a.Fingerprint] = true
		if e, ok := byFP[a.Fingerprint]; ok {
			p.Refresh[e.ID] = a
		} else {
			p.Insert = append(p.Insert, a)
		}
	}
	for _, e := range existing {
		if !seen[e.Fingerprint] {
			p.Resolve = append(p.Resolve, e.ID)
		}
	}
	sort.Slice(p.Resolve, func(i, j int) bool { return p.Resolve[i] < p.Resolve[j] })
	return p
}

// ---- evaluation ----

func trunc(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return string(r)
}

func evaluateAlerts(ctx context.Context, db *pgxpool.Pool, st alertSettings) ([]activeAlert, error) {
	var out []activeAlert

	states := []string{"failed", "stale"}
	if st.IncludeNever {
		states = append(states, "never")
	}
	rows, err := db.Query(ctx, `SELECT id, source_id, source_name, name, state, last_success, last_message, coalesce(last_status,'') FROM (`+wlSQL+`) y
		WHERE state = ANY($2) OR (state = 'never' AND last_status = 'failed')`, st.StaleHours, states)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, sid, sname, name, state, msg, lastStatus string
		var last *time.Time
		if err := rows.Scan(&id, &sid, &sname, &name, &state, &last, &msg, &lastStatus); err != nil {
			rows.Close()
			return nil, err
		}
		// wlSQL reports "never" for a workload with no success yet, even when its
		// first attempt failed. That is a failure, not a coverage gap.
		if state == "never" && lastStatus == "failed" {
			state = "failed"
		}
		a := activeAlert{Fingerprint: "wl:" + id + ":" + state, SourceID: sid}
		switch state {
		case "failed":
			a.Type, a.Severity = "backup_failed", "critical"
			a.Title = fmt.Sprintf("Backup failed: %s (%s)", name, sname)
			a.Detail = trunc(msg, 300)
		case "stale":
			a.Type, a.Severity = "backup_stale", "warning"
			a.Title = fmt.Sprintf("No recent successful backup: %s (%s)", name, sname)
			if last != nil {
				a.Detail = "last success " + last.UTC().Format("2006-01-02 15:04 UTC")
			}
		default:
			a.Type, a.Severity = "backup_never", "warning"
			a.Title = fmt.Sprintf("Never backed up: %s (%s)", name, sname)
		}
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = db.Query(ctx, `SELECT bs.id::text, bs.name, p.node, p.name, p.used_bytes, p.total_bytes
		FROM storage_pools p JOIN backup_sources bs ON bs.id=p.source_id AND bs.enabled
		WHERE p.total_bytes>0 AND p.used_bytes*100.0/p.total_bytes >= $1`, st.StoragePct)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sid, sname, node, name string
		var used, total int64
		if err := rows.Scan(&sid, &sname, &node, &name, &used, &total); err != nil {
			rows.Close()
			return nil, err
		}
		pct := float64(used) * 100 / float64(total)
		sev := "warning"
		if pct >= 95 {
			sev = "critical"
		}
		out = append(out, activeAlert{Fingerprint: "st:" + sid + ":" + node + ":" + name, Type: "storage_high", Severity: sev, SourceID: sid,
			Title:  fmt.Sprintf("Storage %.0f%% full: %s (%s)", pct, name, sname),
			Detail: fmt.Sprintf("%.1f of %.1f GB used", float64(used)/1e9, float64(total)/1e9)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = db.Query(ctx, `SELECT id::text, name, last_error FROM backup_sources WHERE enabled AND connection_status='error'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sid, name, le string
		if err := rows.Scan(&sid, &name, &le); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, activeAlert{Fingerprint: "src:" + sid, Type: "source_down", Severity: "critical", SourceID: sid,
			Title: "Source unreachable: " + name, Detail: trunc(le, 300)})
	}
	rows.Close()
	return out, rows.Err()
}

// syncAlerts reconciles the alerts table with active. It returns the alerts that
// were resolved in this run. Nothing is resolved if active could not be computed
// (the caller passes err != nil in that case and never calls this).
func syncAlerts(ctx context.Context, db *pgxpool.Pool, active []activeAlert) (resolved []alertRow, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Serialise concurrent evaluators (e.g. a second poller by mistake).
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727002)`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id, fingerprint, state FROM alerts WHERE state <> 'resolved'`)
	if err != nil {
		return nil, err
	}
	var ex []existingAlert
	for rows.Next() {
		var e existingAlert
		if err := rows.Scan(&e.ID, &e.Fingerprint, &e.State); err != nil {
			rows.Close()
			return nil, err
		}
		ex = append(ex, e)
	}
	rows.Close()
	p := planAlerts(ex, active)
	for _, a := range p.Insert {
		var sid any
		if a.SourceID != "" {
			sid = a.SourceID
		}
		if _, err := tx.Exec(ctx, `INSERT INTO alerts(fingerprint,type,severity,source_id,title,detail) VALUES($1,$2,$3,$4,$5,$6)`,
			a.Fingerprint, a.Type, a.Severity, sid, a.Title, a.Detail); err != nil {
			return nil, err
		}
	}
	for id, a := range p.Refresh {
		if _, err := tx.Exec(ctx, `UPDATE alerts SET last_seen=now(),severity=$2,title=$3,detail=$4 WHERE id=$1`, id, a.Severity, a.Title, a.Detail); err != nil {
			return nil, err
		}
	}
	for _, id := range p.Resolve {
		var r alertRow
		if err := tx.QueryRow(ctx, `UPDATE alerts SET state='resolved',resolved_at=now() WHERE id=$1
			RETURNING id,type,severity,title,detail,notify_count`, id).Scan(&r.ID, &r.Type, &r.Severity, &r.Title, &r.Detail, &r.NotifyCount); err != nil {
			return nil, err
		}
		resolved = append(resolved, r)
	}
	return resolved, tx.Commit(ctx)
}

type alertRow struct {
	ID          int64
	Type        string
	Severity    string
	Title       string
	Detail      string
	NotifyCount int
}

// pendingNotifications returns open alerts that still need a message: never
// notified, or unacknowledged and due for a reminder.
func pendingNotifications(ctx context.Context, db *pgxpool.Pool, repeatHours int) ([]alertRow, error) {
	rows, err := db.Query(ctx, `SELECT id,type,severity,title,detail,notify_count FROM alerts
		WHERE state='open' AND (notified_at IS NULL OR ($1 > 0 AND notified_at < now() - make_interval(hours => $1)))
		ORDER BY (severity='critical') DESC, first_seen`, repeatHours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alertRow
	for rows.Next() {
		var r alertRow
		if err := rows.Scan(&r.ID, &r.Type, &r.Severity, &r.Title, &r.Detail, &r.NotifyCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func formatAlerts(newA, remind, resolved []alertRow) notice {
	const maxLines = 25
	var b strings.Builder
	line := func(prefix string, rs []alertRow) {
		for i, r := range rs {
			if i == maxLines {
				fmt.Fprintf(&b, "  … and %d more\n", len(rs)-maxLines)
				break
			}
			sev := strings.ToUpper(r.Severity)
			if prefix == "RESOLVED" {
				sev = "OK"
			}
			fmt.Fprintf(&b, "  [%s] %s", sev, r.Title)
			if r.Detail != "" && prefix != "RESOLVED" {
				fmt.Fprintf(&b, " — %s", r.Detail)
			}
			b.WriteString("\n")
		}
	}
	sections := []struct {
		name string
		rs   []alertRow
	}{{"NEW", newA}, {"STILL OPEN", remind}, {"RESOLVED", resolved}}
	var parts []string
	for _, s := range sections {
		if len(s.rs) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s (%d):\n", s.name, len(s.rs))
		line(s.name, s.rs)
		parts = append(parts, fmt.Sprintf("%d %s", len(s.rs), strings.ToLower(s.name)))
	}
	crit := 0
	for _, r := range append(append([]alertRow{}, newA...), remind...) {
		if r.Severity == "critical" {
			crit++
		}
	}
	subj := "[Backup Fleet Monitor] " + strings.Join(parts, ", ")
	if crit > 0 {
		subj = "[Backup Fleet Monitor] CRITICAL: " + strings.Join(parts, ", ")
	}
	items := func(rs []alertRow, state string) []map[string]any {
		var o []map[string]any
		for _, r := range rs {
			o = append(o, map[string]any{"id": r.ID, "type": r.Type, "severity": r.Severity, "title": r.Title, "detail": r.Detail, "state": state})
		}
		return o
	}
	payload := map[string]any{"alerts": append(append(items(newA, "new"), items(remind, "open")...), items(resolved, "resolved")...)}
	return notice{Subject: subj, Text: subj + "\n\n" + b.String(), Payload: payload}
}

// alertsTick runs one evaluation + notification cycle (called from the poller).
func alertsTick(ctx context.Context, db *pgxpool.Pool) {
	st := loadAlertSettings(ctx, db)
	if !st.Enabled {
		return
	}
	active, err := evaluateAlerts(ctx, db, st)
	if err != nil {
		fmt.Println("alerts: evaluate:", err) // keep existing alerts untouched
		return
	}
	resolved, err := syncAlerts(ctx, db, active)
	if err != nil {
		fmt.Println("alerts: sync:", err)
		return
	}
	pend, err := pendingNotifications(ctx, db, st.RepeatHours)
	if err != nil {
		fmt.Println("alerts: pending:", err)
		return
	}
	var newA, remind []alertRow
	for _, r := range pend {
		if r.NotifyCount == 0 {
			newA = append(newA, r)
		} else {
			remind = append(remind, r)
		}
	}
	var rec []alertRow
	if st.NotifyRecovery {
		for _, r := range resolved {
			if r.NotifyCount > 0 {
				rec = append(rec, r)
			}
		}
	}
	if len(newA)+len(remind)+len(rec) == 0 {
		return
	}
	ok, total := dispatch(ctx, db, formatAlerts(newA, remind, rec))
	if total == 0 || ok == 0 {
		return // keep notified_at NULL so the next cycle retries
	}
	for _, r := range append(newA, remind...) {
		db.Exec(ctx, `UPDATE alerts SET notified_at=now(),notify_count=notify_count+1 WHERE id=$1`, r.ID)
	}
}

// ---- HTTP handlers ----

func (a *App) alertsList(w http.ResponseWriter, r *http.Request, u *User) {
	q := r.URL.Query()
	state := q.Get("state")
	lim := 200
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 1000 {
		lim = v
	}
	cond := "state <> 'resolved'"
	switch state {
	case "all":
		cond = "true"
	case "open", "acknowledged", "resolved":
		cond = "state = '" + state + "'" // constant from the switch, not user text
	}
	rows, err := a.db.Query(r.Context(), `SELECT a.id,a.type,a.severity,coalesce(b.name,''),a.title,a.detail,a.state,a.first_seen,a.last_seen,a.resolved_at,a.acknowledged_at,a.acknowledged_by,a.notify_count
		FROM alerts a LEFT JOIN backup_sources b ON b.id=a.source_id WHERE `+cond+`
		ORDER BY (a.state='resolved'), (a.severity='critical') DESC, a.last_seen DESC LIMIT $1`, lim)
	if err != nil {
		apiErr(w, 500, "db", "query failed")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var typ, sev, src, title, detail, st, ackBy string
		var first, last time.Time
		var res, ack *time.Time
		var nc int
		if rows.Scan(&id, &typ, &sev, &src, &title, &detail, &st, &first, &last, &res, &ack, &ackBy, &nc) == nil {
			out = append(out, map[string]any{"id": id, "type": typ, "severity": sev, "source": src, "title": title, "detail": detail, "state": st,
				"firstSeen": first, "lastSeen": last, "resolvedAt": res, "acknowledgedAt": ack, "acknowledgedBy": ackBy, "notifyCount": nc})
		}
	}
	writeJSON(w, 200, out)
}

func (a *App) alertAction(newState string) func(http.ResponseWriter, *http.Request, *User) {
	return func(w http.ResponseWriter, r *http.Request, u *User) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			apiErr(w, 404, "not_found", "no such alert")
			return
		}
		var tag int64
		if newState == "acknowledged" {
			t, e := a.db.Exec(r.Context(), `UPDATE alerts SET state='acknowledged',acknowledged_at=now(),acknowledged_by=$2 WHERE id=$1 AND state='open'`, id, u.Username)
			tag, err = t.RowsAffected(), e
		} else {
			t, e := a.db.Exec(r.Context(), `UPDATE alerts SET state='resolved',resolved_at=now() WHERE id=$1 AND state<>'resolved'`, id)
			tag, err = t.RowsAffected(), e
		}
		if err != nil {
			apiErr(w, 500, "db", "update failed")
			return
		}
		if tag == 0 {
			apiErr(w, 409, "conflict", "alert not found or not in a state that allows this")
			return
		}
		uid := u.ID
		audit(r.Context(), a.db, r, &uid, u.Username, "alert."+strings.TrimSuffix(newState, "d"), "alert", strconv.FormatInt(id, 10), "success", nil)
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}

func (a *App) alertSettingsGet(w http.ResponseWriter, r *http.Request, u *User) {
	writeJSON(w, 200, loadAlertSettings(r.Context(), a.db))
}

func (a *App) alertSettingsPut(w http.ResponseWriter, r *http.Request, u *User) {
	s := loadAlertSettings(r.Context(), a.db)
	if !decode(w, r, &s) {
		return
	}
	if msg := s.validate(); msg != "" {
		apiErr(w, 400, "validation", msg)
		return
	}
	if err := saveSetting(r.Context(), a.db, "alerts", s); err != nil {
		apiErr(w, 500, "db", "save failed")
		return
	}
	uid := u.ID
	audit(r.Context(), a.db, r, &uid, u.Username, "settings.alerts", "settings", "alerts", "success", map[string]any{"settings": s})
	writeJSON(w, 200, s)
}
