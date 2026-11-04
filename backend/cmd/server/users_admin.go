package main

import (
	"context"
	"crypto/rand"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// User administration beyond create/disable: edit name and role, re-enable, admin
// password reset (temporary password, forced change), revoke sessions, delete.
// Guards: nobody can act on their own account in a way that locks them out, the
// last active super_admin is protected, and touching a super_admin requires
// roles.manage.

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)

// validNewPassword returns "" when pw is acceptable for username.
func validNewPassword(pw, username string) string {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < 12:
		return "password must be at least 12 characters"
	case n > 128:
		return "password must be at most 128 characters"
	case strings.EqualFold(pw, username):
		return "password must not equal the username"
	}
	first := []rune(pw)[0]
	same := true
	for _, c := range pw {
		if c != first {
			same = false
			break
		}
	}
	if same {
		return "password must not be a single repeated character"
	}
	return ""
}

const tempPwAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

// tempPassword returns a random 16-character password without look-alike characters.
func tempPassword() (string, error) {
	b := make([]byte, 16)
	max := big.NewInt(int64(len(tempPwAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = tempPwAlphabet[n.Int64()]
	}
	return string(b), nil
}

type targetUser struct {
	ID     string
	Name   string
	Role   string
	Active bool
}

func (a *App) userAud(r *http.Request, u *User, action, target, outcome string, d map[string]any) {
	uid := u.ID
	audit(r.Context(), a.db, r, &uid, u.Username, action, "user", target, outcome, d)
}

// lockTarget loads the target inside tx with a row lock. ok=false means not found.
func lockTarget(ctx context.Context, tx pgx.Tx, id string) (t targetUser, ok bool) {
	if !uuidRe.MatchString(id) {
		return t, false
	}
	err := tx.QueryRow(ctx, `SELECT u.id::text,u.username,ro.name,u.active FROM users u JOIN roles ro ON ro.id=u.role_id WHERE u.id=$1 FOR UPDATE OF u`, id).Scan(&t.ID, &t.Name, &t.Role, &t.Active)
	return t, err == nil
}

func activeSuperAdmins(ctx context.Context, tx pgx.Tx) int {
	var n int
	tx.QueryRow(ctx, `SELECT count(*) FROM users u JOIN roles ro ON ro.id=u.role_id WHERE ro.name='super_admin' AND u.active`).Scan(&n)
	return n
}

// adminGuard enforces the rules shared by every action on another user.
// It writes the error response and returns false when the action is refused.
func (a *App) adminGuard(w http.ResponseWriter, r *http.Request, u *User, t targetUser, allowSelf bool) bool {
	if t.ID == u.ID && !allowSelf {
		apiErr(w, 409, "self_action", "you cannot do this to your own account")
		return false
	}
	if t.Role == "super_admin" && !u.Perms["roles.manage"] {
		a.userAud(r, u, "authz.denied", t.ID, "denied", map[string]any{"reason": "super_admin target"})
		apiErr(w, 403, "forbidden", "only a super admin may modify a super admin")
		return false
	}
	return true
}

func (a *App) userUpdate(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	var in struct {
		DisplayName *string `json:"displayName"`
		Role        *string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.DisplayName != nil && utf8.RuneCountInString(strings.TrimSpace(*in.DisplayName)) > 100 {
		apiErr(w, 400, "validation", "displayName too long (max 100)")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		apiErr(w, 500, "internal", "tx failed")
		return
	}
	defer tx.Rollback(r.Context())
	t, ok := lockTarget(r.Context(), tx, id)
	if !ok {
		apiErr(w, 404, "not_found", "user not found")
		return
	}
	roleChange := in.Role != nil && *in.Role != t.Role
	if !a.adminGuard(w, r, u, t, !roleChange) { // editing your own display name is fine
		return
	}
	detail := map[string]any{"username": t.Name}
	if roleChange {
		var rid int
		if err := tx.QueryRow(r.Context(), `SELECT id FROM roles WHERE name=$1`, *in.Role).Scan(&rid); err != nil {
			apiErr(w, 400, "validation", "unknown role")
			return
		}
		if *in.Role == "super_admin" && !u.Perms["roles.manage"] {
			apiErr(w, 403, "forbidden", "only a super admin may grant super_admin")
			return
		}
		if t.Role == "super_admin" && t.Active && activeSuperAdmins(r.Context(), tx) <= 1 {
			a.userAud(r, u, "user.update", id, "denied", map[string]any{"reason": "last super admin"})
			apiErr(w, 409, "last_super_admin", "cannot change the role of the last active super admin")
			return
		}
		if _, err := tx.Exec(r.Context(), `UPDATE users SET role_id=$2,updated_at=now() WHERE id=$1`, id, rid); err != nil {
			apiErr(w, 500, "db", "update failed")
			return
		}
		detail["roleFrom"], detail["roleTo"] = t.Role, *in.Role
	}
	if in.DisplayName != nil {
		if _, err := tx.Exec(r.Context(), `UPDATE users SET display_name=$2,updated_at=now() WHERE id=$1`, id, strings.TrimSpace(*in.DisplayName)); err != nil {
			apiErr(w, 500, "db", "update failed")
			return
		}
		detail["displayNameChanged"] = true
	}
	if err := tx.Commit(r.Context()); err != nil {
		apiErr(w, 500, "internal", "commit failed")
		return
	}
	a.userAud(r, u, "user.update", id, "success", detail)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) userEnable(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		apiErr(w, 500, "internal", "tx failed")
		return
	}
	defer tx.Rollback(r.Context())
	t, ok := lockTarget(r.Context(), tx, id)
	if !ok {
		apiErr(w, 404, "not_found", "user not found")
		return
	}
	if !a.adminGuard(w, r, u, t, false) {
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE users SET active=true,updated_at=now() WHERE id=$1`, id); err != nil {
		apiErr(w, 500, "db", "update failed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		apiErr(w, 500, "internal", "commit failed")
		return
	}
	a.userAud(r, u, "user.enable", id, "success", map[string]any{"username": t.Name})
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (a *App) userResetPassword(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		apiErr(w, 500, "internal", "tx failed")
		return
	}
	defer tx.Rollback(r.Context())
	t, ok := lockTarget(r.Context(), tx, id)
	if !ok {
		apiErr(w, 404, "not_found", "user not found")
		return
	}
	if !a.adminGuard(w, r, u, t, false) {
		return
	}
	pw, err := tempPassword()
	if err != nil {
		apiErr(w, 500, "internal", "could not generate password")
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE users SET password_hash=$2,must_change_password=true,updated_at=now() WHERE id=$1`, id, hashPassword(pw)); err != nil {
		apiErr(w, 500, "db", "update failed")
		return
	}
	tx.Exec(r.Context(), `UPDATE user_sessions SET revoked=true WHERE user_id=$1`, id)
	if err := tx.Commit(r.Context()); err != nil {
		apiErr(w, 500, "internal", "commit failed")
		return
	}
	a.userAud(r, u, "user.password_reset", id, "success", map[string]any{"username": t.Name}) // never the password
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"temporaryPassword": pw})
}

func (a *App) userRevokeSessions(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		apiErr(w, 500, "internal", "tx failed")
		return
	}
	defer tx.Rollback(r.Context())
	t, ok := lockTarget(r.Context(), tx, id)
	if !ok {
		apiErr(w, 404, "not_found", "user not found")
		return
	}
	if !a.adminGuard(w, r, u, t, false) {
		return
	}
	tag, err := tx.Exec(r.Context(), `UPDATE user_sessions SET revoked=true WHERE user_id=$1 AND NOT revoked`, id)
	if err != nil || tx.Commit(r.Context()) != nil {
		apiErr(w, 500, "db", "update failed")
		return
	}
	a.userAud(r, u, "user.sessions_revoke", id, "success", map[string]any{"username": t.Name, "count": tag.RowsAffected()})
	writeJSON(w, 200, map[string]int64{"revoked": tag.RowsAffected()})
}

func (a *App) userDelete(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		apiErr(w, 500, "internal", "tx failed")
		return
	}
	defer tx.Rollback(r.Context())
	t, ok := lockTarget(r.Context(), tx, id)
	if !ok {
		apiErr(w, 404, "not_found", "user not found")
		return
	}
	if !a.adminGuard(w, r, u, t, false) {
		return
	}
	if t.Active {
		apiErr(w, 409, "still_active", "disable the user before deleting")
		return
	}
	if _, err := tx.Exec(r.Context(), `DELETE FROM users WHERE id=$1`, id); err != nil {
		apiErr(w, 500, "db", "delete failed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		apiErr(w, 500, "internal", "commit failed")
		return
	}
	a.userAud(r, u, "user.delete", id, "success", map[string]any{"username": t.Name, "role": t.Role})
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
