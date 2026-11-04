package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Notification channels. The config of each channel (SMTP password, Telegram bot
// token, webhook URL/Authorization) is stored AES-GCM encrypted and is never
// returned by the API; responses carry a masked summary only.

const (
	chEmail    = "email"
	chTelegram = "telegram"
	chWebhook  = "webhook"
)

type chanConfig struct {
	// email
	Host     string   `json:"host,omitempty"`
	Port     int      `json:"port,omitempty"`
	Security string   `json:"security,omitempty"` // starttls | tls | none
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from,omitempty"`
	To       []string `json:"to,omitempty"`
	// telegram
	BotToken string `json:"botToken,omitempty"`
	ChatID   string `json:"chatId,omitempty"`
	// webhook
	URL           string `json:"url,omitempty"`
	Authorization string `json:"authorization,omitempty"`
}

// chanIn is the API input. Pointers distinguish "not sent" from "empty" so a
// PATCH can keep stored secrets when the field is omitted or blank.
type chanIn struct {
	Name          *string   `json:"name"`
	Kind          *string   `json:"kind"`
	Enabled       *bool     `json:"enabled"`
	Host          *string   `json:"host"`
	Port          *int      `json:"port"`
	Security      *string   `json:"security"`
	Username      *string   `json:"username"`
	Password      *string   `json:"password"`
	From          *string   `json:"from"`
	To            *[]string `json:"to"`
	BotToken      *string   `json:"botToken"`
	ChatID        *string   `json:"chatId"`
	URL           *string   `json:"url"`
	Authorization *string   `json:"authorization"`
}

var (
	// notifyHTTP is replaced in tests.
	notifyHTTP = &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			ResponseHeaderTimeout: 15 * time.Second,
		},
		// Never follow redirects: they could carry the Authorization header or
		// bounce the request to an internal address.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	telegramBase = "https://api.telegram.org"
)

func hasCtl(s string) bool { return strings.ContainsAny(s, "\r\n\x00") }

func allowHTTPWebhook() bool { return os.Getenv("NOTIFY_ALLOW_HTTP") == "true" }

// mergeConfig applies in on top of old (old may be zero for a new channel) and
// validates the result for kind. Empty secret inputs keep the stored value.
func mergeConfig(kind string, old chanConfig, in *chanIn) (chanConfig, string) {
	c := old
	setS := func(dst *string, src *string, secret bool) {
		if src == nil {
			return
		}
		v := strings.TrimSpace(*src)
		if v == "" && secret {
			return // keep
		}
		*dst = v
	}
	switch kind {
	case chEmail:
		setS(&c.Host, in.Host, false)
		setS(&c.Security, in.Security, false)
		setS(&c.Username, in.Username, false)
		setS(&c.Password, in.Password, true)
		setS(&c.From, in.From, false)
		if in.Port != nil {
			c.Port = *in.Port
		}
		if in.To != nil {
			c.To = nil
			for _, a := range *in.To {
				if a = strings.TrimSpace(a); a != "" {
					c.To = append(c.To, a)
				}
			}
		}
		if c.Security == "" {
			c.Security = "starttls"
		}
		if c.Port == 0 {
			c.Port = map[string]int{"tls": 465, "starttls": 587, "none": 25}[c.Security]
		}
		switch {
		case c.Host == "" || hasCtl(c.Host) || strings.ContainsAny(c.Host, "/ "):
			return c, "host required"
		case c.Port < 1 || c.Port > 65535:
			return c, "port must be 1..65535"
		case c.Security != "starttls" && c.Security != "tls" && c.Security != "none":
			return c, "security must be starttls, tls or none"
		case c.From == "" || hasCtl(c.From) || !strings.Contains(c.From, "@"):
			return c, "from must be an email address"
		case len(c.To) == 0 || len(c.To) > 20:
			return c, "1..20 recipients required"
		case hasCtl(c.Username) || hasCtl(c.Password):
			return c, "invalid credentials"
		case c.Username != "" && c.Password == "":
			return c, "password required with username"
		case c.Username != "" && c.Security == "none":
			return c, "authentication requires starttls or tls"
		}
		for _, a := range c.To {
			if hasCtl(a) || !strings.Contains(a, "@") {
				return c, "invalid recipient address"
			}
		}
	case chTelegram:
		setS(&c.BotToken, in.BotToken, true)
		setS(&c.ChatID, in.ChatID, false)
		if c.BotToken == "" || hasCtl(c.BotToken) || strings.ContainsAny(c.BotToken, "/ ?#") {
			return c, "botToken required"
		}
		if c.ChatID == "" || hasCtl(c.ChatID) || len(c.ChatID) > 64 {
			return c, "chatId required"
		}
	case chWebhook:
		setS(&c.URL, in.URL, true)
		setS(&c.Authorization, in.Authorization, true)
		if in.Authorization != nil && strings.TrimSpace(*in.Authorization) == "-" {
			c.Authorization = "" // explicit clear
		}
		u, err := url.Parse(c.URL)
		if err != nil || u.Host == "" || u.User != nil || hasCtl(c.URL) {
			return c, "valid url required"
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && allowHTTPWebhook()) {
			return c, "url must be https"
		}
		if hasCtl(c.Authorization) {
			return c, "invalid authorization value"
		}
	default:
		return c, "kind must be email, telegram or webhook"
	}
	return c, ""
}

// describe returns the non-secret summary shown in the UI.
func describe(kind string, c chanConfig) map[string]any {
	switch kind {
	case chEmail:
		return map[string]any{"host": c.Host, "port": c.Port, "security": c.Security, "username": c.Username, "from": c.From, "to": c.To, "hasPassword": c.Password != ""}
	case chTelegram:
		return map[string]any{"chatId": c.ChatID, "hasBotToken": c.BotToken != ""}
	case chWebhook:
		host := ""
		if u, err := url.Parse(c.URL); err == nil {
			host = u.Scheme + "://" + u.Host + "/…" // path often embeds a secret token
		}
		return map[string]any{"url": host, "hasAuthorization": c.Authorization != ""}
	}
	return map[string]any{}
}

// scrubErr removes URLs (which can embed secrets) from transport errors.
func scrubErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

type notice struct {
	Subject string
	Text    string
	Payload map[string]any // extra JSON for webhooks
}

func sendNotice(ctx context.Context, kind string, c chanConfig, n notice) error {
	switch kind {
	case chEmail:
		return sendEmail(ctx, c, n)
	case chTelegram:
		return sendTelegram(ctx, c, n)
	case chWebhook:
		return sendWebhook(ctx, c, n)
	}
	return errors.New("unknown channel kind")
}

func httpPost(ctx context.Context, target string, hdr map[string]string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(body))
	if err != nil {
		return errors.New("bad url")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "backup-fleet-monitor")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := notifyHTTP.Do(req)
	if err != nil {
		return errors.New(scrubErr(err))
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func sendTelegram(ctx context.Context, c chanConfig, n notice) error {
	text := n.Text
	if utf8.RuneCountInString(text) > 4000 {
		text = string([]rune(text)[:3990]) + "\n…"
	}
	body, _ := json.Marshal(map[string]any{"chat_id": c.ChatID, "text": text, "disable_web_page_preview": true})
	return httpPost(ctx, telegramBase+"/bot"+c.BotToken+"/sendMessage", nil, body)
}

func sendWebhook(ctx context.Context, c chanConfig, n notice) error {
	p := map[string]any{"text": n.Text, "subject": n.Subject}
	for k, v := range n.Payload {
		p[k] = v
	}
	body, _ := json.Marshal(p)
	h := map[string]string{}
	if c.Authorization != "" {
		h["Authorization"] = c.Authorization
	}
	return httpPost(ctx, c.URL, h, body)
}

func buildEmail(c chanConfig, n notice, now time.Time) []byte {
	subj := mime.QEncoding.Encode("utf-8", strings.NewReplacer("\r", " ", "\n", " ").Replace(n.Subject))
	body := strings.ReplaceAll(strings.ReplaceAll(n.Text, "\r\n", "\n"), "\n", "\r\n")
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n",
		c.From, strings.Join(c.To, ", "), subj, now.Format(time.RFC1123Z), body)
	return b.Bytes()
}

func sendEmail(ctx context.Context, c chanConfig, n notice) error {
	addr := net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
	d := &net.Dialer{Timeout: 15 * time.Second}
	tcfg := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	if c.Security == "tls" {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tcfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return errors.New(scrubErr(err))
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return errors.New(scrubErr(err))
	}
	defer cl.Close()
	if c.Security == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return errors.New("server does not offer STARTTLS")
		}
		if err := cl.StartTLS(tcfg); err != nil {
			return errors.New(scrubErr(err))
		}
	}
	if c.Username != "" {
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return errors.New("smtp auth failed: " + scrubErr(err))
		}
	}
	if err := cl.Mail(c.From); err != nil {
		return errors.New(scrubErr(err))
	}
	for _, r := range c.To {
		if err := cl.Rcpt(r); err != nil {
			return errors.New(scrubErr(err))
		}
	}
	w, err := cl.Data()
	if err != nil {
		return errors.New(scrubErr(err))
	}
	if _, err := w.Write(buildEmail(c, n, time.Now())); err != nil {
		return errors.New(scrubErr(err))
	}
	if err := w.Close(); err != nil {
		return errors.New(scrubErr(err))
	}
	return cl.Quit()
}

// ---- storage + dispatch ----

type chanRow struct {
	ID      string
	Name    string
	Kind    string
	Enabled bool
	Cfg     chanConfig
}

func loadChannel(ctx context.Context, db *pgxpool.Pool, id string) (*chanRow, error) {
	var r chanRow
	var enc []byte
	if err := db.QueryRow(ctx, `SELECT id::text,name,kind,enabled,config_enc FROM notification_channels WHERE id=$1`, id).Scan(&r.ID, &r.Name, &r.Kind, &r.Enabled, &enc); err != nil {
		return nil, err
	}
	pt, err := decrypt(enc)
	if err != nil {
		return nil, errors.New("cannot decrypt channel config (SECRET_KEY changed?)")
	}
	if err := json.Unmarshal(pt, &r.Cfg); err != nil {
		return nil, err
	}
	return &r, nil
}

func enabledChannels(ctx context.Context, db *pgxpool.Pool) []chanRow {
	rows, err := db.Query(ctx, `SELECT id::text,name,kind,config_enc FROM notification_channels WHERE enabled ORDER BY created_at`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []chanRow
	for rows.Next() {
		var r chanRow
		var enc []byte
		if rows.Scan(&r.ID, &r.Name, &r.Kind, &enc) != nil {
			continue
		}
		pt, err := decrypt(enc)
		if err != nil || json.Unmarshal(pt, &r.Cfg) != nil {
			markChannel(ctx, db, r.ID, errors.New("cannot decrypt channel config"))
			continue
		}
		r.Enabled = true
		out = append(out, r)
	}
	return out
}

func markChannel(ctx context.Context, db *pgxpool.Pool, id string, err error) {
	if err == nil {
		db.Exec(ctx, `UPDATE notification_channels SET last_status='ok',last_error='',last_sent_at=now() WHERE id=$1`, id)
		return
	}
	db.Exec(ctx, `UPDATE notification_channels SET last_status='error',last_error=$2 WHERE id=$1`, id, err.Error())
}

// dispatch sends n to every enabled channel and reports how many succeeded.
func dispatch(ctx context.Context, db *pgxpool.Pool, n notice) (ok, total int) {
	chs := enabledChannels(ctx, db)
	for _, c := range chs {
		cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := sendNotice(cctx, c.Kind, c.Cfg, n)
		cancel()
		markChannel(ctx, db, c.ID, err)
		if err == nil {
			ok++
		} else {
			fmt.Println("notify", c.Name, "failed:", err)
		}
	}
	return ok, len(chs)
}

// ---- HTTP handlers ----

func (a *App) channelsList(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(r.Context(), `SELECT id::text,name,kind,enabled,config_enc,last_status,last_error,last_sent_at,created_at FROM notification_channels ORDER BY lower(name)`)
	if err != nil {
		apiErr(w, 500, "db", "query failed")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, kind, ls, le string
		var en bool
		var enc []byte
		var sent *time.Time
		var created time.Time
		if rows.Scan(&id, &name, &kind, &en, &enc, &ls, &le, &sent, &created) != nil {
			continue
		}
		var cfg chanConfig
		if pt, err := decrypt(enc); err == nil {
			json.Unmarshal(pt, &cfg)
		}
		out = append(out, map[string]any{"id": id, "name": name, "kind": kind, "enabled": en, "config": describe(kind, cfg),
			"lastStatus": ls, "lastError": le, "lastSentAt": sent, "createdAt": created})
	}
	writeJSON(w, 200, out)
}

func (a *App) channelAud(r *http.Request, u *User, action, id, outcome string, d map[string]any) {
	uid := u.ID
	audit(r.Context(), a.db, r, &uid, u.Username, action, "notification_channel", id, outcome, d)
}

func (a *App) channelCreate(w http.ResponseWriter, r *http.Request, u *User) {
	var in chanIn
	if !decode(w, r, &in) {
		return
	}
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if name == "" || len(name) > 100 {
		apiErr(w, 400, "validation", "name required (max 100)")
		return
	}
	kind := ""
	if in.Kind != nil {
		kind = *in.Kind
	}
	cfg, msg := mergeConfig(kind, chanConfig{}, &in)
	if msg != "" {
		apiErr(w, 400, "validation", msg)
		return
	}
	pt, _ := json.Marshal(cfg)
	enc, err := encrypt(pt)
	if err != nil {
		apiErr(w, 500, "crypto", "cannot encrypt config")
		return
	}
	enabled := in.Enabled == nil || *in.Enabled
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO notification_channels(name,kind,config_enc,enabled) VALUES($1,$2,$3,$4) RETURNING id::text`, name, kind, enc, enabled).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "notification_channels_name_uq") {
			apiErr(w, 409, "conflict", "channel name already exists")
			return
		}
		apiErr(w, 500, "db", "insert failed")
		return
	}
	a.channelAud(r, u, "channel.create", id, "success", map[string]any{"name": name, "kind": kind})
	writeJSON(w, 201, map[string]string{"id": id})
}

func (a *App) channelUpdate(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		apiErr(w, 404, "not_found", "no such channel")
		return
	}
	var in chanIn
	if !decode(w, r, &in) {
		return
	}
	cur, err := loadChannel(r.Context(), a.db, id)
	if err != nil {
		apiErr(w, 404, "not_found", "no such channel")
		return
	}
	cfg, msg := mergeConfig(cur.Kind, cur.Cfg, &in)
	if msg != "" {
		apiErr(w, 400, "validation", msg)
		return
	}
	name := cur.Name
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if name == "" || len(name) > 100 {
			apiErr(w, 400, "validation", "name required (max 100)")
			return
		}
	}
	enabled := cur.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	pt, _ := json.Marshal(cfg)
	enc, err := encrypt(pt)
	if err != nil {
		apiErr(w, 500, "crypto", "cannot encrypt config")
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE notification_channels SET name=$2,config_enc=$3,enabled=$4,updated_at=now() WHERE id=$1`, id, name, enc, enabled); err != nil {
		if strings.Contains(err.Error(), "notification_channels_name_uq") {
			apiErr(w, 409, "conflict", "channel name already exists")
			return
		}
		apiErr(w, 500, "db", "update failed")
		return
	}
	a.channelAud(r, u, "channel.update", id, "success", map[string]any{"name": name, "enabled": enabled})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) channelDelete(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		apiErr(w, 404, "not_found", "no such channel")
		return
	}
	var name string
	a.db.QueryRow(r.Context(), `SELECT name FROM notification_channels WHERE id=$1`, id).Scan(&name)
	tag, _ := a.db.Exec(r.Context(), `DELETE FROM notification_channels WHERE id=$1`, id)
	if tag.RowsAffected() == 0 {
		apiErr(w, 404, "not_found", "no such channel")
		return
	}
	a.channelAud(r, u, "channel.delete", id, "success", map[string]any{"name": name})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) channelTest(w http.ResponseWriter, r *http.Request, u *User) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		apiErr(w, 404, "not_found", "no such channel")
		return
	}
	c, err := loadChannel(r.Context(), a.db, id)
	if err != nil {
		apiErr(w, 404, "not_found", "no such channel")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	txt := "Test notification from Backup Fleet Monitor.\nSent by " + u.Username + " at " + time.Now().UTC().Format(time.RFC3339)
	err = sendNotice(ctx, c.Kind, c.Cfg, notice{Subject: "[Backup Fleet Monitor] Test notification", Text: txt, Payload: map[string]any{"test": true}})
	markChannel(r.Context(), a.db, id, err)
	if err != nil {
		a.channelAud(r, u, "channel.test", id, "failure", map[string]any{"error": err.Error()})
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.channelAud(r, u, "channel.test", id, "success", nil)
	writeJSON(w, 200, map[string]any{"ok": true})
}
