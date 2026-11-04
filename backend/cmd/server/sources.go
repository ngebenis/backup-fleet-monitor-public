package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)
var tokenIDRe = regexp.MustCompile(`^[^\s!@]+@[^\s!@]+![^\s!]+$`)

type srcIn struct {
	Name        *string `json:"name"`
	Platform    *string `json:"platform"`
	BaseURL     *string `json:"baseUrl"`
	Environment *string `json:"environment"`
	Interval    *int    `json:"pollIntervalSeconds"`
	AuthType    *string `json:"authType"`
	TokenID     *string `json:"tokenId"`
	TokenSecret *string `json:"tokenSecret"`
	Username    *string `json:"username"`
	Password    *string `json:"password"`
	TLSVerify   *bool   `json:"tlsVerify"`
	CAPem       *string `json:"caPem"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func validBase(s string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", false
	}
	return "https://" + u.Host, true
}

func credsFrom(in *srcIn, platform string) (*pveCreds, string) {
	// Push-ingest sources never hold credentials: the collector runs on the
	// source host and authenticates with a token issued by sourceToken below.
	if platform == "veeam_push" {
		return &pveCreds{AuthType: "ingest"}, ""
	}
	if platform == "veeam" || platform == "synology_abb" {
		if str(in.Username) == "" || in.Password == nil || *in.Password == "" {
			return nil, "username and password required"
		}
		return &pveCreds{AuthType: "password", Username: str(in.Username), Password: *in.Password}, ""
	}
	switch str(in.AuthType) {
	case "token":
		if !tokenIDRe.MatchString(str(in.TokenID)) {
			return nil, "tokenId must look like user@realm!tokenname"
		}
		if str(in.TokenSecret) == "" {
			return nil, "tokenSecret required"
		}
		return &pveCreds{AuthType: "token", TokenID: str(in.TokenID), TokenSecret: str(in.TokenSecret)}, ""
	case "password":
		if !strings.Contains(str(in.Username), "@") || in.Password == nil || *in.Password == "" {
			return nil, "username (user@realm) and password required"
		}
		return &pveCreds{AuthType: "password", Username: str(in.Username), Password: *in.Password}, ""
	}
	return nil, "authType must be token or password"
}

func (a *App) aud(r *http.Request, u *User, action, resID, outcome string, d map[string]any) {
	id := u.ID
	audit(r.Context(), a.db, r, &id, u.Username, action, "source", resID, outcome, d)
}

type srcOut struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Platform  string     `json:"platform"`
	BaseURL   string     `json:"baseUrl"`
	Env       string     `json:"environment"`
	Interval  int        `json:"pollIntervalSeconds"`
	Enabled   bool       `json:"enabled"`
	Status    string     `json:"connectionStatus"`
	AuthType  string     `json:"authType"`
	TLSVerify bool       `json:"tlsVerify"`
	HasCA     bool       `json:"hasCa"`
	LastPoll  *time.Time `json:"lastPollAt"`
	LastError string     `json:"lastError"`
	Created   time.Time  `json:"createdAt"`
	Workloads int        `json:"workloads"`
}

func (a *App) sourcesList(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(r.Context(), `SELECT s.id::text,s.name,s.platform,s.base_url,s.environment,s.poll_interval_seconds,s.enabled,s.connection_status,
		s.auth_type,s.tls_verify,(s.ca_pem<>''),s.last_poll_at,s.last_error,s.created_at,
		(SELECT count(*) FROM backup_workloads w WHERE w.source_id=s.id AND w.status<>'removed' AND NOT w.template)
		FROM backup_sources s ORDER BY lower(s.name)`)
	if err != nil {
		apiErr(w, 500, "db", "query failed")
		return
	}
	defer rows.Close()
	out := []srcOut{}
	for rows.Next() {
		var s srcOut
		if rows.Scan(&s.ID, &s.Name, &s.Platform, &s.BaseURL, &s.Env, &s.Interval, &s.Enabled, &s.Status, &s.AuthType, &s.TLSVerify, &s.HasCA, &s.LastPoll, &s.LastError, &s.Created, &s.Workloads) == nil {
			out = append(out, s)
		}
	}
	writeJSON(w, 200, out)
}

func (a *App) sourceCreate(w http.ResponseWriter, r *http.Request, u *User) {
	var in srcIn
	if !decode(w, r, &in) {
		return
	}
	name := str(in.Name)
	if name == "" || len(name) > 100 {
		apiErr(w, 400, "validation", "name required (max 100)")
		return
	}
	platform := str(in.Platform)
	if platform != "proxmox" && platform != "veeam" && platform != "veeam_push" && platform != "synology_abb" {
		apiErr(w, 400, "not_supported", "platform must be proxmox, veeam, veeam_push or synology_abb")
		return
	}
	base, ok := validBase(str(in.BaseURL))
	if !ok {
		apiErr(w, 400, "validation", "baseUrl must be https://host[:port]")
		return
	}
	iv := 1800
	if in.Interval != nil {
		iv = *in.Interval
	}
	if iv < 300 || iv > 86400 {
		apiErr(w, 400, "validation", "pollIntervalSeconds must be 300..86400")
		return
	}
	c, msg := credsFrom(&in, platform)
	if c == nil {
		apiErr(w, 400, "validation", msg)
		return
	}
	tlsv := true
	if in.TLSVerify != nil {
		tlsv = *in.TLSVerify
	}
	if len(str(in.CAPem)) > 20000 {
		apiErr(w, 400, "validation", "caPem too large")
		return
	}
	pt, _ := json.Marshal(c)
	enc, err := encrypt(pt)
	if err != nil {
		apiErr(w, 500, "crypto", "cannot encrypt credentials")
		return
	}
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO backup_sources(name,platform,base_url,environment,poll_interval_seconds,auth_type,secret_enc,tls_verify,ca_pem)
		VALUES($1,$9,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, name, base, str(in.Environment), iv, c.AuthType, enc, tlsv, str(in.CAPem), platform).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "backup_sources_name_uq") {
			apiErr(w, 409, "conflict", "source name already exists")
			return
		}
		apiErr(w, 500, "db", "insert failed")
		return
	}
	out := map[string]any{"id": id}
	// Push sources get a one-time token. It is shown once and never stored in clear.
	if platform == "veeam_push" {
		token, hash := newToken()
		if _, err := a.db.Exec(r.Context(), `INSERT INTO ingest_tokens(source_id,label,token_hash)
			VALUES($1,$2,$3)`, id, name, hash); err != nil {
			apiErr(w, 500, "db", "token insert failed")
			return
		}
		out["ingestToken"] = token
	}
	a.aud(r, u, "source.create", id, "success", map[string]any{"name": name, "platform": platform, "authType": c.AuthType, "baseUrl": base})
	writeJSON(w, 201, out)
}

func (a *App) sourceUpdate(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		apiErr(w, 404, "not_found", "no such source")
		return
	}
	var in srcIn
	if !decode(w, r, &in) {
		return
	}
	var base *string
	if in.BaseURL != nil {
		b, ok := validBase(*in.BaseURL)
		if !ok {
			apiErr(w, 400, "validation", "baseUrl must be https://host[:port]")
			return
		}
		base = &b
	}
	if in.Interval != nil && (*in.Interval < 300 || *in.Interval > 86400) {
		apiErr(w, 400, "validation", "pollIntervalSeconds must be 300..86400")
		return
	}
	if in.Name != nil && (str(in.Name) == "" || len(str(in.Name)) > 100) {
		apiErr(w, 400, "validation", "name required (max 100)")
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE backup_sources SET name=COALESCE($2,name),environment=COALESCE($3,environment),
		poll_interval_seconds=COALESCE($4,poll_interval_seconds),tls_verify=COALESCE($5,tls_verify),ca_pem=COALESCE($6,ca_pem),
		base_url=COALESCE($7,base_url),updated_at=now() WHERE id=$1`, id, in.Name, in.Environment, in.Interval, in.TLSVerify, in.CAPem, base)
	if err != nil {
		if strings.Contains(err.Error(), "backup_sources_name_uq") {
			apiErr(w, 409, "conflict", "source name already exists")
			return
		}
		apiErr(w, 500, "db", "update failed")
		return
	}
	if tag.RowsAffected() == 0 {
		apiErr(w, 404, "not_found", "no such source")
		return
	}
	rotated := false
	if in.AuthType != nil {
		var plat string
		a.db.QueryRow(r.Context(), `SELECT platform FROM backup_sources WHERE id=$1`, id).Scan(&plat)
		c, msg := credsFrom(&in, plat)
		if c == nil {
			apiErr(w, 400, "validation", msg)
			return
		}
		pt, _ := json.Marshal(c)
		enc, err := encrypt(pt)
		if err != nil {
			apiErr(w, 500, "crypto", "cannot encrypt credentials")
			return
		}
		a.db.Exec(r.Context(), `UPDATE backup_sources SET secret_enc=$2,auth_type=$3,connection_status='not_tested',last_poll_at=NULL WHERE id=$1`, id, enc, c.AuthType)
		rotated = true
	}
	a.aud(r, u, "source.update", id, "success", map[string]any{"credentialsReplaced": rotated})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) sourceEnabled(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !uuidRe.MatchString(id) || !decode(w, r, &in) {
		if !uuidRe.MatchString(id) {
			apiErr(w, 404, "not_found", "no such source")
		}
		return
	}
	need := "sources.enable"
	if !in.Enabled {
		need = "sources.disable"
	}
	if !u.Perms[need] {
		apiErr(w, 403, "forbidden", "permission denied")
		return
	}
	tag, _ := a.db.Exec(r.Context(), `UPDATE backup_sources SET enabled=$2,updated_at=now() WHERE id=$1`, id, in.Enabled)
	if tag.RowsAffected() == 0 {
		apiErr(w, 404, "not_found", "no such source")
		return
	}
	act := "source.disable"
	if in.Enabled {
		act = "source.enable"
	}
	a.aud(r, u, act, id, "success", nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) sourceDelete(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		apiErr(w, 404, "not_found", "no such source")
		return
	}
	var name string
	a.db.QueryRow(r.Context(), `SELECT name FROM backup_sources WHERE id=$1`, id).Scan(&name)
	tag, _ := a.db.Exec(r.Context(), `DELETE FROM backup_sources WHERE id=$1`, id)
	if tag.RowsAffected() == 0 {
		apiErr(w, 404, "not_found", "no such source")
		return
	}
	a.aud(r, u, "source.delete", id, "success", map[string]any{"name": name})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) sourceTest(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		apiErr(w, 404, "not_found", "no such source")
		return
	}
	s, err := loadSource(r.Context(), a.db, id)
	if err != nil {
		apiErr(w, 404, "not_found", "no such source")
		return
	}
	// Push sources have nothing to dial: the collector does the connecting and
	// reports health on each ingest. Rather than returning a bare ok, surface the
	// last ingest so the button tells the operator something real — and record the
	// audit entry, otherwise the click leaves no trace and looks like a dead
	// button.
	if s.Platform == "veeam_push" {
		var lastAt *time.Time
		var lastStatus, lastErr string
		lastStatus = "none"
		a.db.QueryRow(r.Context(), `SELECT finished_at, status, coalesce(error,'')
			FROM source_poll_runs WHERE source_id=$1 AND finished_at IS NOT NULL
			ORDER BY finished_at DESC LIMIT 1`,
			id).Scan(&lastAt, &lastStatus, &lastErr)

		note := "push source: the collector on the Veeam host pushes data; there is no endpoint to dial"
		info := map[string]any{"platform": "veeam_push", "note": note}
		if lastAt == nil {
			info["lastIngest"] = nil
			note += " — no ingest received yet"
		} else {
			info["lastIngest"] = lastAt.Format(time.RFC3339)
			info["lastIngestStatus"] = lastStatus
			if lastErr != "" {
				info["lastIngestError"] = lastErr
			}
			note += fmt.Sprintf(" — last ingest %s (%s)", lastAt.Format(time.RFC3339), lastStatus)
		}
		info["note"] = note

		a.db.Exec(r.Context(), `UPDATE backup_sources SET connection_status=$2,last_error=$3 WHERE id=$1`,
			id, map[bool]string{true: "ok", false: "error"}[lastStatus == "" || lastStatus == "success"], lastErr)
		a.aud(r, u, "source.test", id, "success", info)
		writeJSON(w, 200, map[string]any{"ok": true, "info": info})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	info, err := testSource(ctx, s)
	if err != nil {
		a.db.Exec(r.Context(), `UPDATE backup_sources SET connection_status='error',last_error=$2 WHERE id=$1`, id, err.Error())
		a.aud(r, u, "source.test", id, "failure", map[string]any{"error": err.Error()})
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.db.Exec(r.Context(), `UPDATE backup_sources SET connection_status='ok',last_error='' WHERE id=$1`, id)
	a.aud(r, u, "source.test", id, "success", info)
	writeJSON(w, 200, map[string]any{"ok": true, "info": info})
}

func (a *App) sourcePoll(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		apiErr(w, 404, "not_found", "no such source")
		return
	}
	// A push source is fed by the collector on the Veeam host. Clearing
	// last_poll_at would look like a queued poll, but the poller skips
	// veeam_push entirely — nothing would ever pick it up, and the UI's
	// "Last poll" column would go blank. Say so instead of pretending.
	var platform string
	if err := a.db.QueryRow(r.Context(), `SELECT platform FROM backup_sources WHERE id=$1 AND enabled`, id).Scan(&platform); err != nil {
		apiErr(w, 404, "not_found", "no such enabled source")
		return
	}
	if platform == "veeam_push" {
		apiErr(w, 409, "push_source", "this source is push-based: the collector on the Veeam host runs every 15 minutes; trigger it there (Start-ScheduledTask -TaskName bm-collector)")
		return
	}
	tag, _ := a.db.Exec(r.Context(), `UPDATE backup_sources SET last_poll_at=NULL WHERE id=$1 AND enabled`, id)
	if tag.RowsAffected() == 0 {
		apiErr(w, 404, "not_found", "no such enabled source")
		return
	}
	a.aud(r, u, "source.poll_requested", id, "success", nil)
	writeJSON(w, 202, map[string]bool{"queued": true})
}
