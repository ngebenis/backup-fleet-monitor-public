package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	db           *pgxpool.Pool
	cookieSecure bool
	sessionTTL   time.Duration
	loginLimit   *limiter
}

type User struct {
	ID         string          `json:"id"`
	Username   string          `json:"username"`
	Display    string          `json:"displayName"`
	Role       string          `json:"role"`
	Language   string          `json:"language"`
	Theme      string          `json:"theme"`
	Timezone   string          `json:"timezone"`
	MustChange bool            `json:"mustChangePassword"`
	Perms      map[string]bool `json:"-"`
	PermList   []string        `json:"permissions"`
	SessionID  string          `json:"-"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, code int, c, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": c, "message": msg}})
}

const cookieName = "bm_session"

func (a *App) loadUser(ctx context.Context, r *http.Request) *User {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	u := &User{Perms: map[string]bool{}}
	var roleID int
	err = a.db.QueryRow(ctx, `SELECT u.id::text,u.username,u.display_name,ro.name,ro.id,u.language,u.theme,u.timezone,u.must_change_password,s.id::text
		FROM user_sessions s JOIN users u ON u.id=s.user_id JOIN roles ro ON ro.id=u.role_id
		WHERE s.token_hash=$1 AND NOT s.revoked AND s.expires_at>now() AND u.active`, tokenHash(c.Value)).
		Scan(&u.ID, &u.Username, &u.Display, &u.Role, &roleID, &u.Language, &u.Theme, &u.Timezone, &u.MustChange, &u.SessionID)
	if err != nil {
		return nil
	}
	rows, err := a.db.Query(ctx, "SELECT permission FROM role_permissions WHERE role_id=$1", roleID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p string
			rows.Scan(&p)
			u.Perms[p] = true
			u.PermList = append(u.PermList, p)
		}
	}
	a.db.Exec(ctx, "UPDATE user_sessions SET last_seen=now() WHERE id=$1", u.SessionID)
	return u
}

// guard: authn + CSRF header on mutations + optional permission (deny by default).
func (a *App) guard(perm string, h func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.loadUser(r.Context(), r)
		if u == nil {
			apiErr(w, 401, "unauthenticated", "login required")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-Requested-With") != "bm-web" {
			apiErr(w, 403, "csrf", "missing CSRF header")
			return
		}
		if u.MustChange && !strings.HasSuffix(r.URL.Path, "/me/password") && !strings.HasSuffix(r.URL.Path, "/me") && !strings.HasSuffix(r.URL.Path, "/auth/logout") {
			apiErr(w, 403, "password_change_required", "change your password first")
			return
		}
		if perm != "" && !u.Perms[perm] {
			id := u.ID
			audit(r.Context(), a.db, r, &id, u.Username, "authz.denied", "permission", perm, "denied", map[string]any{"path": r.URL.Path})
			apiErr(w, 403, "forbidden", "permission denied")
			return
		}
		h(w, r, u)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		apiErr(w, 400, "bad_request", "invalid JSON body")
		return false
	}
	return true
}

func (a *App) routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, c := context.WithTimeout(r.Context(), 2*time.Second)
		defer c()
		if err := a.db.Ping(ctx); err != nil {
			apiErr(w, 503, "db_down", "database unavailable")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	m.HandleFunc("POST /api/v1/auth/login", a.login)
	m.HandleFunc("POST /api/v1/auth/logout", a.guard("", a.logout))
	m.HandleFunc("GET /api/v1/me", a.guard("", func(w http.ResponseWriter, r *http.Request, u *User) { writeJSON(w, 200, u) }))
	m.HandleFunc("PATCH /api/v1/me/preferences", a.guard("", a.prefs))
	m.HandleFunc("POST /api/v1/me/password", a.guard("", a.changePassword))
	m.HandleFunc("GET /api/v1/me/sessions", a.guard("", a.mySessions))
	m.HandleFunc("DELETE /api/v1/me/sessions/others", a.guard("", a.revokeOthers))
	m.HandleFunc("GET /api/v1/dashboard/summary", a.guard("dashboard.read", a.dashboard))
	m.HandleFunc("GET /api/v1/audit-logs", a.guard("audit.read", a.auditList))
	m.HandleFunc("GET /api/v1/users", a.guard("users.read", a.usersList))
	m.HandleFunc("POST /api/v1/users", a.guard("users.create", a.userCreate))
	m.HandleFunc("PATCH /api/v1/users/{id}/disable", a.guard("users.disable", a.userDisable))
	m.HandleFunc("GET /api/v1/roles", a.guard("roles.read", a.rolesList))
	m.HandleFunc("GET /api/v1/sources", a.guard("sources.read", a.sourcesList))
	m.HandleFunc("POST /api/v1/sources", a.guard("sources.create", a.sourceCreate))
	m.HandleFunc("PATCH /api/v1/sources/{id}", a.guard("sources.update", a.sourceUpdate))
	m.HandleFunc("DELETE /api/v1/sources/{id}", a.guard("sources.delete", a.sourceDelete))
	m.HandleFunc("PATCH /api/v1/sources/{id}/enabled", a.guard("sources.enable", a.sourceEnabled))
	m.HandleFunc("POST /api/v1/sources/{id}/test", a.guard("sources.test", a.sourceTest))
	m.HandleFunc("POST /api/v1/sources/{id}/poll", a.guard("sources.test", a.sourcePoll))
	m.HandleFunc("GET /api/v1/workloads", a.guard("jobs.read", a.workloadsList))
	m.HandleFunc("GET /api/v1/abb/tasks", a.guard("jobs.read", a.abbTasksList))
	m.HandleFunc("GET /api/v1/reports/data", a.guard("jobs.read", a.reportData))
	m.HandleFunc("GET /api/v1/storage", a.guard("jobs.read", a.storageList))
	m.HandleFunc("GET /api/v1/sessions", a.guard("jobs.read", a.sessionsList))
	// Machine-to-machine push ingestion (Bearer token, no cookie, no CSRF).
	m.HandleFunc("POST /api/v1/ingest/veeam", a.ingestVeeam)
	m.HandleFunc("POST /api/v1/sources/{id}/ingest-token", a.guard("sources.update", a.ingestTokenIssue))
	return secHeaders(m)
}

func secHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	ip := clientIP(r)
	// The per-user key includes the client IP so a remote attacker cannot lock
	// a real user out by spamming their username from elsewhere.
	ukey := "u:" + ip + ":" + in.Username
	if !a.loginLimit.allow(ip) || !a.loginLimit.allow(ukey) {
		audit(r.Context(), a.db, r, nil, in.Username, "auth.login", "user", "", "rate_limited", nil)
		apiErr(w, 429, "rate_limited", "too many attempts, retry later")
		return
	}
	var id, hash string
	var active bool
	err := a.db.QueryRow(r.Context(), "SELECT id::text,password_hash,active FROM users WHERE username=$1", in.Username).Scan(&id, &hash, &active)
	if err != nil {
		verifyPassword(in.Password, dummyHash)
		a.loginLimit.fail(ip)
		a.loginLimit.fail(ukey)
		audit(r.Context(), a.db, r, nil, in.Username, "auth.login", "user", "", "failure", nil)
		apiErr(w, 401, "invalid_credentials", "invalid username or password")
		return
	}
	if !verifyPassword(in.Password, hash) || !active {
		a.loginLimit.fail(ip)
		a.loginLimit.fail(ukey)
		audit(r.Context(), a.db, r, &id, in.Username, "auth.login", "user", id, "failure", nil)
		apiErr(w, 401, "invalid_credentials", "invalid username or password")
		return
	}
	a.loginLimit.reset(ukey)
	tok, th := newToken()
	exp := time.Now().Add(a.sessionTTL)
	if _, err := a.db.Exec(r.Context(), "INSERT INTO user_sessions(user_id,token_hash,expires_at,ip,user_agent) VALUES($1,$2,$3,$4,$5)", id, th, exp, ip, r.UserAgent()); err != nil {
		apiErr(w, 500, "internal", "could not create session")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", Expires: exp, HttpOnly: true, Secure: a.cookieSecure, SameSite: http.SameSiteStrictMode})
	audit(r.Context(), a.db, r, &id, in.Username, "auth.login", "user", id, "success", nil)
	u := a.loadUser(r.Context(), withCookie(r, tok))
	writeJSON(w, 200, u)
}

func withCookie(r *http.Request, tok string) *http.Request {
	r2 := r.Clone(r.Context())
	r2.Header.Set("Cookie", cookieName+"="+tok)
	return r2
}

func (a *App) logout(w http.ResponseWriter, r *http.Request, u *User) {
	a.db.Exec(r.Context(), "UPDATE user_sessions SET revoked=true WHERE id=$1", u.SessionID)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cookieSecure, SameSite: http.SameSiteStrictMode})
	audit(r.Context(), a.db, r, &u.ID, u.Username, "auth.logout", "user", u.ID, "success", nil)
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (a *App) prefs(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct{ Language, Theme, Timezone string }
	if !decode(w, r, &in) {
		return
	}
	if in.Language != "" && in.Language != "id" && in.Language != "en" {
		apiErr(w, 400, "validation", "language must be id or en")
		return
	}
	if in.Theme != "" && in.Theme != "light" && in.Theme != "dark" && in.Theme != "system" {
		apiErr(w, 400, "validation", "theme must be light, dark or system")
		return
	}
	if in.Timezone != "" {
		if _, err := time.LoadLocation(in.Timezone); err != nil {
			apiErr(w, 400, "validation", "unknown IANA timezone")
			return
		}
	}
	_, err := a.db.Exec(r.Context(), `UPDATE users SET language=COALESCE(NULLIF($1,''),language), theme=COALESCE(NULLIF($2,''),theme),
		timezone=COALESCE(NULLIF($3,''),timezone), updated_at=now() WHERE id=$4`, in.Language, in.Theme, in.Timezone, u.ID)
	if err != nil {
		apiErr(w, 500, "internal", "update failed")
		return
	}
	writeJSON(w, 200, a.loadUser(r.Context(), r))
}

func (a *App) changePassword(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct{ Current, New string }
	if !decode(w, r, &in) {
		return
	}
	if utf8.RuneCountInString(in.New) < 12 {
		apiErr(w, 400, "validation", "new password must be at least 12 characters")
		return
	}
	var hash string
	a.db.QueryRow(r.Context(), "SELECT password_hash FROM users WHERE id=$1", u.ID).Scan(&hash)
	if !verifyPassword(in.Current, hash) {
		audit(r.Context(), a.db, r, &u.ID, u.Username, "auth.password_change", "user", u.ID, "failure", nil)
		apiErr(w, 400, "invalid_credentials", "current password incorrect")
		return
	}
	a.db.Exec(r.Context(), "UPDATE users SET password_hash=$1,must_change_password=false,updated_at=now() WHERE id=$2", hashPassword(in.New), u.ID)
	a.db.Exec(r.Context(), "UPDATE user_sessions SET revoked=true WHERE user_id=$1 AND id<>$2", u.ID, u.SessionID)
	audit(r.Context(), a.db, r, &u.ID, u.Username, "auth.password_change", "user", u.ID, "success", nil)
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (a *App) mySessions(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(r.Context(), `SELECT id::text,created_at,last_seen,expires_at,ip,user_agent FROM user_sessions
		WHERE user_id=$1 AND NOT revoked AND expires_at>now() ORDER BY last_seen DESC`, u.ID)
	if err != nil {
		apiErr(w, 500, "internal", "query failed")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, ip, ua string
		var c, l, e time.Time
		rows.Scan(&id, &c, &l, &e, &ip, &ua)
		out = append(out, map[string]any{"id": id, "createdAt": c, "lastSeen": l, "expiresAt": e, "ip": ip, "userAgent": ua, "current": id == u.SessionID})
	}
	writeJSON(w, 200, out)
}

func (a *App) revokeOthers(w http.ResponseWriter, r *http.Request, u *User) {
	t, _ := a.db.Exec(r.Context(), "UPDATE user_sessions SET revoked=true WHERE user_id=$1 AND id<>$2 AND NOT revoked", u.ID, u.SessionID)
	audit(r.Context(), a.db, r, &u.ID, u.Username, "auth.session_revoke_others", "user", u.ID, "success", map[string]any{"count": t.RowsAffected()})
	writeJSON(w, 200, map[string]int64{"revoked": t.RowsAffected()})
}

// dashboard: no source data collected yet in this release -> explicit "no data", never fake zeros as success.
func (a *App) auditList(w http.ResponseWriter, r *http.Request, u *User) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	action := r.URL.Query().Get("action")
	rows, err := a.db.Query(r.Context(), `SELECT id,ts,actor_name,action,resource_type,resource_id,outcome,ip FROM audit_logs
		WHERE ($1='' OR action=$1) ORDER BY id DESC LIMIT $2`, action, limit)
	if err != nil {
		apiErr(w, 500, "internal", "query failed")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var ts time.Time
		var actor, act, rt, rid, oc, ip string
		rows.Scan(&id, &ts, &actor, &act, &rt, &rid, &oc, &ip)
		out = append(out, map[string]any{"id": id, "ts": ts, "actor": actor, "action": act, "resourceType": rt, "resourceId": rid, "outcome": oc, "ip": ip})
	}
	writeJSON(w, 200, out)
}

func (a *App) usersList(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(r.Context(), `SELECT u.id::text,u.username,u.display_name,ro.name,u.active,u.created_at FROM users u JOIN roles ro ON ro.id=u.role_id ORDER BY u.username`)
	if err != nil {
		apiErr(w, 500, "internal", "query failed")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, un, dn, role string
		var act bool
		var c time.Time
		rows.Scan(&id, &un, &dn, &role, &act, &c)
		out = append(out, map[string]any{"id": id, "username": un, "displayName": dn, "role": role, "active": act, "createdAt": c})
	}
	writeJSON(w, 200, out)
}

func (a *App) userCreate(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct{ Username, DisplayName, Role, Password string }
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if len(in.Username) < 3 || utf8.RuneCountInString(in.Password) < 12 {
		apiErr(w, 400, "validation", "username >=3 chars and password >=12 chars required")
		return
	}
	if in.Role == "super_admin" && !u.Perms["roles.manage"] {
		apiErr(w, 403, "forbidden", "only super admin may create super admin")
		return
	}
	var id string
	err := a.db.QueryRow(r.Context(), `INSERT INTO users(username,display_name,password_hash,role_id,must_change_password)
		SELECT $1,$2,$3,id,true FROM roles WHERE name=$4 RETURNING id::text`, in.Username, in.DisplayName, hashPassword(in.Password), in.Role).Scan(&id)
	if err != nil {
		audit(r.Context(), a.db, r, &u.ID, u.Username, "user.create", "user", in.Username, "failure", nil)
		if err == pgx.ErrNoRows {
			apiErr(w, 400, "validation", "unknown role")
		} else {
			apiErr(w, 409, "conflict", "could not create user (duplicate username?)")
		}
		return
	}
	audit(r.Context(), a.db, r, &u.ID, u.Username, "user.create", "user", id, "success", map[string]any{"username": in.Username, "role": in.Role})
	writeJSON(w, 201, map[string]string{"id": id})
}

func (a *App) userDisable(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		apiErr(w, 500, "internal", "tx failed")
		return
	}
	defer tx.Rollback(r.Context())
	var role string
	var active bool
	if err := tx.QueryRow(r.Context(), "SELECT ro.name,u.active FROM users u JOIN roles ro ON ro.id=u.role_id WHERE u.id::text=$1 FOR UPDATE OF u", id).Scan(&role, &active); err != nil {
		apiErr(w, 404, "not_found", "user not found")
		return
	}
	if role == "super_admin" && active {
		var n int
		tx.QueryRow(r.Context(), "SELECT count(*) FROM users u JOIN roles ro ON ro.id=u.role_id WHERE ro.name='super_admin' AND u.active").Scan(&n)
		if n <= 1 {
			audit(r.Context(), a.db, r, &u.ID, u.Username, "user.disable", "user", id, "denied", map[string]any{"reason": "last super admin"})
			apiErr(w, 409, "last_super_admin", "cannot disable the last active super admin")
			return
		}
	}
	tx.Exec(r.Context(), "UPDATE users SET active=false,updated_at=now() WHERE id::text=$1", id)
	tx.Exec(r.Context(), "UPDATE user_sessions SET revoked=true WHERE user_id::text=$1", id)
	if err := tx.Commit(r.Context()); err != nil {
		apiErr(w, 500, "internal", "commit failed")
		return
	}
	audit(r.Context(), a.db, r, &u.ID, u.Username, "user.disable", "user", id, "success", nil)
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (a *App) rolesList(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(r.Context(), `SELECT ro.name,ro.description,coalesce(array_agg(rp.permission ORDER BY rp.permission) FILTER (WHERE rp.permission IS NOT NULL),'{}') FROM roles ro LEFT JOIN role_permissions rp ON rp.role_id=ro.id GROUP BY ro.id ORDER BY ro.id`)
	if err != nil {
		apiErr(w, 500, "internal", "query failed")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var n, d string
		var p []string
		rows.Scan(&n, &d, &p)
		out = append(out, map[string]any{"name": n, "description": d, "permissions": p})
	}
	writeJSON(w, 200, out)
}

func bootstrapAdmin(ctx context.Context, db *pgxpool.Pool) {
	var n int
	db.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&n)
	if n > 0 {
		return
	}
	pw := os.Getenv("ADMIN_BOOTSTRAP_PASSWORD")
	if len(pw) < 12 {
		println("no users and ADMIN_BOOTSTRAP_PASSWORD missing/short; skipping bootstrap")
		return
	}
	name := os.Getenv("ADMIN_BOOTSTRAP_USER")
	if name == "" {
		name = "admin"
	}
	_, err := db.Exec(ctx, `INSERT INTO users(username,display_name,password_hash,role_id,must_change_password)
		SELECT $1,'Super Admin',$2,id,true FROM roles WHERE name='super_admin'`, name, hashPassword(pw))
	if err != nil {
		println("bootstrap failed:", err.Error())
		return
	}
	audit(ctx, db, nil, nil, "system", "user.bootstrap", "user", name, "success", nil)
	println("bootstrap admin created:", name)
}
