package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	return r.RemoteAddr
}

// audit writes an append-only record. detail must never carry secrets.
func audit(ctx context.Context, db *pgxpool.Pool, r *http.Request, actorID *string, actor, action, resType, resID, outcome string, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	b, _ := json.Marshal(detail)
	rid, ip, ua := "", "", ""
	if r != nil {
		rid, ip, ua = r.Header.Get("X-Request-ID"), clientIP(r), r.UserAgent()
	}
	_, _ = db.Exec(ctx, `INSERT INTO audit_logs(actor_id,actor_name,action,resource_type,resource_id,outcome,ip,user_agent,request_id,detail)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, actorID, actor, action, resType, resID, outcome, ip, ua, rid, b)
}
