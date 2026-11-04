package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Data retention. Each table has a "keep N days" setting (0 = keep forever).
// audit_logs is append-only; the purge runs in a transaction that sets
// bm.audit_purge, which the table trigger accepts for DELETE only (see 017).

type retentionSettings struct {
	AuditDays    int `json:"auditDays"`
	PollRunsDays int `json:"pollRunsDays"`
	AbbEventDays int `json:"abbEventDays"`
	SessionsDays int `json:"sessionsDays"`
	AlertsDays   int `json:"alertsDays"`
}

func defaultRetention() retentionSettings {
	return retentionSettings{AuditDays: 365, PollRunsDays: 30, AbbEventDays: 180, SessionsDays: 365, AlertsDays: 180}
}

// Minimums protect what the dashboards and coverage logic still read.
var retentionMin = map[string]int{"auditDays": 90, "pollRunsDays": 7, "abbEventDays": 30, "sessionsDays": 90, "alertsDays": 30}

const retentionMax = 3650

func (s retentionSettings) validate() string {
	for k, v := range map[string]int{"auditDays": s.AuditDays, "pollRunsDays": s.PollRunsDays, "abbEventDays": s.AbbEventDays, "sessionsDays": s.SessionsDays, "alertsDays": s.AlertsDays} {
		if v == 0 {
			continue
		}
		if v < retentionMin[k] || v > retentionMax {
			return fmt.Sprintf("%s must be 0 (keep forever) or %d..%d", k, retentionMin[k], retentionMax)
		}
	}
	return ""
}

type retTarget struct {
	Key   string
	Days  int
	Where string // predicate on table alias "t", uses $1 = days
	Table string
	Audit bool
}

func (s retentionSettings) targets() []retTarget {
	return []retTarget{
		{"auditLogs", s.AuditDays, `t.ts < now() - make_interval(days => $1)`, "audit_logs", true},
		{"pollRuns", s.PollRunsDays, `t.started_at < now() - make_interval(days => $1)`, "source_poll_runs", false},
		{"abbEvents", s.AbbEventDays, `t.log_time < now() - make_interval(days => $1)`, "abb_events", false},
		{"backupSessions", s.SessionsDays, `coalesce(t.ended_at,t.started_at) < now() - make_interval(days => $1)`, "backup_sessions", false},
		{"alerts", s.AlertsDays, `t.state='resolved' AND t.resolved_at < now() - make_interval(days => $1)`, "alerts", false},
	}
}

// Expired login sessions are always removed (not configurable): they are dead weight.
const sessionsWhere = `(t.expires_at < now() - make_interval(days => $1) OR (t.revoked AND t.last_seen < now() - make_interval(days => $1)))`
const sessionsGraceDays = 7

const purgeBatch = 5000

// purgeTable deletes in batches so one run never holds a long lock.
func purgeTable(ctx context.Context, db *pgxpool.Pool, tb retTarget, days int) (int64, error) {
	var total int64
	for {
		sql := fmt.Sprintf(`DELETE FROM %[1]s WHERE ctid IN (SELECT t.ctid FROM %[1]s t WHERE %[2]s LIMIT %[3]d)`, tb.Table, tb.Where, purgeBatch)
		tx, err := db.Begin(ctx)
		if err != nil {
			return total, err
		}
		if tb.Audit {
			if _, err := tx.Exec(ctx, `SELECT set_config('bm.audit_purge','on',true)`); err != nil {
				tx.Rollback(ctx)
				return total, err
			}
		}
		tag, err := tx.Exec(ctx, sql, days)
		if err != nil {
			tx.Rollback(ctx)
			return total, err
		}
		if err := tx.Commit(ctx); err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < purgeBatch {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
	}
}

func countRows(ctx context.Context, db *pgxpool.Pool, tb retTarget, days int) int64 {
	var n int64
	db.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s t WHERE %s`, tb.Table, tb.Where), days).Scan(&n)
	return n
}

// runRetention purges (or, with dryRun, only counts) what is past its retention.
func runRetention(ctx context.Context, db *pgxpool.Pool, s retentionSettings, dryRun bool) (map[string]int64, error) {
	out := map[string]int64{}
	for _, tb := range s.targets() {
		if tb.Days == 0 {
			continue
		}
		if dryRun {
			out[tb.Key] = countRows(ctx, db, tb, tb.Days)
			continue
		}
		n, err := purgeTable(ctx, db, tb, tb.Days)
		out[tb.Key] = n
		if err != nil {
			return out, fmt.Errorf("%s: %w", tb.Key, err)
		}
	}
	st := retTarget{Key: "loginSessions", Table: "user_sessions", Where: sessionsWhere}
	if dryRun {
		out[st.Key] = countRows(ctx, db, st, sessionsGraceDays)
		return out, nil
	}
	n, err := purgeTable(ctx, db, st, sessionsGraceDays)
	out[st.Key] = n
	return out, err
}

func loadRetention(ctx context.Context, db *pgxpool.Pool) retentionSettings {
	s := defaultRetention()
	loadSetting(ctx, db, "retention", &s)
	return s
}

// retentionTick runs the purge at most once a day (called from the poller).
func retentionTick(ctx context.Context, db *pgxpool.Pool) {
	var last struct {
		At time.Time `json:"at"`
	}
	loadSetting(ctx, db, "retention_last_run", &last)
	if time.Since(last.At) < 23*time.Hour {
		return
	}
	res, err := runRetention(ctx, db, loadRetention(ctx, db), false)
	recordRetention(ctx, db, nil, "system", res, err)
}

func recordRetention(ctx context.Context, db *pgxpool.Pool, r *http.Request, actor string, res map[string]int64, err error) {
	outcome := "success"
	d := map[string]any{"deleted": res}
	if err != nil {
		outcome, d["error"] = "failure", err.Error()
		fmt.Println("retention:", err)
	}
	saveSetting(ctx, db, "retention_last_run", map[string]any{"at": time.Now().UTC(), "deleted": res, "ok": err == nil})
	audit(ctx, db, r, nil, actor, "retention.purge", "system", "", outcome, d)
}

// ---- HTTP handlers ----

func (a *App) retentionGet(w http.ResponseWriter, r *http.Request, u *User) {
	ctx := r.Context()
	var last map[string]any
	loadSetting(ctx, a.db, "retention_last_run", &last)
	counts := map[string]int64{}
	for _, tb := range append(loadRetention(ctx, a.db).targets(), retTarget{Key: "loginSessions", Table: "user_sessions"}) {
		var n int64
		a.db.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, tb.Table)).Scan(&n)
		counts[tb.Key] = n
	}
	writeJSON(w, 200, map[string]any{"settings": loadRetention(ctx, a.db), "minimums": retentionMin, "lastRun": last, "rows": counts})
}

func (a *App) retentionPut(w http.ResponseWriter, r *http.Request, u *User) {
	s := loadRetention(r.Context(), a.db)
	if !decode(w, r, &s) {
		return
	}
	if msg := s.validate(); msg != "" {
		apiErr(w, 400, "validation", msg)
		return
	}
	if err := saveSetting(r.Context(), a.db, "retention", s); err != nil {
		apiErr(w, 500, "db", "save failed")
		return
	}
	uid := u.ID
	audit(r.Context(), a.db, r, &uid, u.Username, "settings.retention", "settings", "retention", "success", map[string]any{"settings": s})
	writeJSON(w, 200, s)
}

func (a *App) retentionRun(w http.ResponseWriter, r *http.Request, u *User) {
	dry := r.URL.Query().Get("dryRun") == "true"
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	res, err := runRetention(ctx, a.db, loadRetention(ctx, a.db), dry)
	if !dry {
		recordRetention(ctx, a.db, r, u.Username, res, err)
	}
	if err != nil {
		apiErr(w, 500, "retention", "purge failed; see server log")
		return
	}
	writeJSON(w, 200, map[string]any{"dryRun": dry, "deleted": res})
}
