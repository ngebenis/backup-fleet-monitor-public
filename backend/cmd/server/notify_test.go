package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sp(s string) *string { return &s }

func TestMergeConfigEmail(t *testing.T) {
	c, msg := mergeConfig(chEmail, chanConfig{}, &chanIn{Host: sp("smtp.example.com"), From: sp("bm@example.com"), To: &[]string{"a@example.com", " ", "b@example.com"}, Username: sp("u"), Password: sp("secret")})
	if msg != "" {
		t.Fatal(msg)
	}
	if c.Port != 587 || c.Security != "starttls" || len(c.To) != 2 {
		t.Fatalf("defaults: %+v", c)
	}
	// blank password on update keeps the stored one
	c2, msg := mergeConfig(chEmail, c, &chanIn{Password: sp("")})
	if msg != "" || c2.Password != "secret" {
		t.Fatalf("secret must be kept: %q %q", c2.Password, msg)
	}
	for name, in := range map[string]*chanIn{
		"header injection": {From: sp("x@example.com\r\nBcc: evil@example.com")},
		"bad recipient":    {To: &[]string{"no-at-sign"}},
		"auth over none":   {Security: sp("none")},
		"bad port":         {Port: func() *int { p := 70000; return &p }()},
	} {
		if _, msg := mergeConfig(chEmail, c, in); msg == "" {
			t.Errorf("%s should be rejected", name)
		}
	}
}

func TestMergeConfigWebhookAndTelegram(t *testing.T) {
	if _, msg := mergeConfig(chWebhook, chanConfig{}, &chanIn{URL: sp("http://hooks.example.com/x")}); msg == "" {
		t.Error("http webhook must be rejected by default")
	}
	t.Setenv("NOTIFY_ALLOW_HTTP", "true")
	if _, msg := mergeConfig(chWebhook, chanConfig{}, &chanIn{URL: sp("http://hooks.example.com/x")}); msg != "" {
		t.Errorf("http allowed by env: %s", msg)
	}
	if _, msg := mergeConfig(chWebhook, chanConfig{}, &chanIn{URL: sp("https://user:pw@hooks.example.com/x")}); msg == "" {
		t.Error("userinfo in url must be rejected")
	}
	if _, msg := mergeConfig(chWebhook, chanConfig{}, &chanIn{URL: sp("ftp://x")}); msg == "" {
		t.Error("non-http scheme rejected")
	}
	c, msg := mergeConfig(chWebhook, chanConfig{}, &chanIn{URL: sp("https://hooks.example.com/T0/secret"), Authorization: sp("Bearer abc")})
	if msg != "" {
		t.Fatal(msg)
	}
	d := describe(chWebhook, c)
	if strings.Contains(d["url"].(string), "secret") || strings.Contains(d["url"].(string), "T0") {
		t.Errorf("describe must hide the url path: %v", d)
	}
	if d["hasAuthorization"] != true {
		t.Error("hasAuthorization")
	}
	if _, msg := mergeConfig(chTelegram, chanConfig{}, &chanIn{BotToken: sp("123:abc/def"), ChatID: sp("1")}); msg == "" {
		t.Error("slash in bot token rejected")
	}
	tc, msg := mergeConfig(chTelegram, chanConfig{}, &chanIn{BotToken: sp("123:abc"), ChatID: sp("-100")})
	if msg != "" {
		t.Fatal(msg)
	}
	if td := describe(chTelegram, tc); td["hasBotToken"] != true || td["chatId"] != "-100" {
		t.Errorf("%v", td)
	}
	if _, msg := mergeConfig("sms", chanConfig{}, &chanIn{}); msg == "" {
		t.Error("unknown kind")
	}
}

func TestSendWebhook(t *testing.T) {
	var gotAuth, gotBody, gotCT string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotAuth, gotBody, gotCT = r.Header.Get("Authorization"), string(b), r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	old := notifyHTTP
	notifyHTTP = srv.Client()
	notifyHTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { notifyHTTP = old }()

	err := sendWebhook(context.Background(), chanConfig{URL: srv.URL, Authorization: "Bearer tok"}, notice{Subject: "s", Text: "hello", Payload: map[string]any{"k": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok" || gotCT != "application/json" || !strings.Contains(gotBody, `"text":"hello"`) || !strings.Contains(gotBody, `"k":1`) {
		t.Fatalf("auth=%q ct=%q body=%s", gotAuth, gotCT, gotBody)
	}
}

func TestSendWebhookErrorsAndRedirects(t *testing.T) {
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/redir" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(500)
		io.WriteString(w, "boom")
	}))
	defer srv.Close()
	old := notifyHTTP
	notifyHTTP = srv.Client()
	notifyHTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { notifyHTTP = old }()

	if err := sendWebhook(context.Background(), chanConfig{URL: srv.URL + "/x"}, notice{}); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("500 must be an error: %v", err)
	}
	before := hits
	if err := sendWebhook(context.Background(), chanConfig{URL: srv.URL + "/redir"}, notice{}); err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("redirect must be an error, not followed: %v", err)
	}
	if hits-before != 1 {
		t.Errorf("redirect was followed (%d requests)", hits-before)
	}
}

func TestTelegramErrorDoesNotLeakToken(t *testing.T) {
	old := telegramBase
	defer func() { telegramBase = old }()
	telegramBase = "https://127.0.0.1:1" // connection refused
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := sendTelegram(ctx, chanConfig{BotToken: "SECRETTOKEN123", ChatID: "1"}, notice{Text: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "SECRETTOKEN123") {
		t.Fatalf("token leaked in error: %v", err)
	}
}

func TestSendTelegramPayload(t *testing.T) {
	var path, body string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		path, body = r.URL.Path, string(b)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	oldC, oldB := notifyHTTP, telegramBase
	notifyHTTP, telegramBase = srv.Client(), srv.URL
	defer func() { notifyHTTP, telegramBase = oldC, oldB }()
	long := strings.Repeat("é", 5000)
	if err := sendTelegram(context.Background(), chanConfig{BotToken: "123:abc", ChatID: "-5"}, notice{Text: long}); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:abc/sendMessage" || !strings.Contains(body, `"chat_id":"-5"`) {
		t.Fatalf("%s %s", path, body[:60])
	}
	if len([]rune(body)) > 4200 {
		t.Errorf("message not truncated to Telegram's limit (%d runes)", len([]rune(body)))
	}
}

func TestBuildEmailNoHeaderInjection(t *testing.T) {
	c := chanConfig{From: "bm@example.com", To: []string{"a@example.com"}}
	raw := string(buildEmail(c, notice{Subject: "hi\r\nBcc: evil@example.com", Text: "line1\nline2"}, time.Unix(0, 0)))
	head := raw[:strings.Index(raw, "\r\n\r\n")]
	if strings.Contains(head, "\r\nBcc:") {
		t.Fatalf("header injection:\n%s", head)
	}
	if !strings.Contains(raw, "line1\r\nline2") {
		t.Error("body must use CRLF")
	}
}
