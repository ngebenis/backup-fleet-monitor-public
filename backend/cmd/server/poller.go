package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func beat(ctx context.Context, db *pgxpool.Pool) {
	if _, err := db.Exec(ctx, `INSERT INTO service_heartbeat(service,last_beat,version) VALUES('poller',now(),$1)
		ON CONFLICT (service) DO UPDATE SET last_beat=now(),version=$1`, version); err == nil {
		os.WriteFile("/tmp/beat", []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
	} else {
		fmt.Println("heartbeat:", err)
	}
}

// pollOne runs one polling cycle for a source. Read-only toward the source.
func pollOne(ctx context.Context, db *pgxpool.Pool, id string) {
	s, err := loadSource(ctx, db, id)
	if err != nil {
		return
	}
	var runID int64
	db.QueryRow(ctx, `INSERT INTO source_poll_runs(source_id) VALUES($1) RETURNING id`, id).Scan(&runID)
	pctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var counts map[string]any
	var warns []string
	switch s.Platform {
	case "proxmox":
		counts, warns, err = pollProxmox(pctx, db, s)
	case "synology_abb":
		counts, warns, err = pollSynology(pctx, db, s)
	case "veeam":
		if strings.HasPrefix(s.LastError, "PROBE") {
			counts, warns, err = probeVeeam(pctx, s)
		} else {
			counts, warns, err = pollVeeam(pctx, db, s)
		}
	default:
		err = fmt.Errorf("platform %s: adapter not implemented", s.Platform)
	}
	status, msg := "success", ""
	if err != nil {
		status, msg = "failed", err.Error()
	} else if len(warns) > 0 {
		status, msg = "partial", strings.Join(warns, "; ")
	}
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	cj, _ := json.Marshal(counts)
	if cj == nil || string(cj) == "null" {
		cj = []byte("{}")
	}
	db.Exec(ctx, `UPDATE source_poll_runs SET finished_at=now(),status=$2,error=$3,counts=$4::jsonb WHERE id=$1`, runID, status, msg, string(cj))
	conn := "ok"
	if status == "failed" {
		conn = "error"
	}
	db.Exec(ctx, `UPDATE backup_sources SET last_poll_at=now(),connection_status=$2,last_error=$3 WHERE id=$1`, id, conn, msg)
	fmt.Println("poll", s.Name, status, string(cj))
}

func runPoller(ctx context.Context, db *pgxpool.Pool) {
	fmt.Println("poller started", version)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	var lastAlerts time.Time
	for {
		beat(ctx, db)
		// Alert evaluation every 5 minutes; retention at most once a day.
		if time.Since(lastAlerts) >= 5*time.Minute {
			lastAlerts = time.Now()
			alertsTick(ctx, db)
			retentionTick(ctx, db)
			beat(ctx, db)
		}
		nodes.refreshIfStale(ctx, db)
		rows, err := db.Query(ctx, `SELECT id::text FROM backup_sources
			WHERE enabled AND platform<>'veeam_push' AND secret_enc IS NOT NULL AND (last_poll_at IS NULL OR last_poll_at < now() - make_interval(secs => poll_interval_seconds))`)
		if err == nil {
			var ids []string
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				pollOne(ctx, db, id)
				beat(ctx, db)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
