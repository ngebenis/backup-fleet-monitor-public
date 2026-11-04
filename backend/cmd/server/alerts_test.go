package main

import (
	"strings"
	"testing"
)

func TestPlanAlerts(t *testing.T) {
	existing := []existingAlert{
		{ID: 1, Fingerprint: "wl:a:failed", State: "open"},
		{ID: 2, Fingerprint: "wl:b:stale", State: "acknowledged"},
		{ID: 3, Fingerprint: "src:x", State: "open"},
	}
	active := []activeAlert{
		{Fingerprint: "wl:a:failed", Title: "still failing"}, // persists
		{Fingerprint: "wl:c:failed", Title: "new one"},       // new
		{Fingerprint: "wl:c:failed", Title: "duplicate"},     // dup in input is ignored
	}
	p := planAlerts(existing, active)
	if len(p.Insert) != 1 || p.Insert[0].Fingerprint != "wl:c:failed" || p.Insert[0].Title != "new one" {
		t.Fatalf("insert: %+v", p.Insert)
	}
	if a, ok := p.Refresh[1]; !ok || a.Title != "still failing" || len(p.Refresh) != 1 {
		t.Fatalf("refresh: %+v", p.Refresh)
	}
	if len(p.Resolve) != 2 || p.Resolve[0] != 2 || p.Resolve[1] != 3 {
		t.Fatalf("resolve (acknowledged alerts resolve too): %+v", p.Resolve)
	}
}

func TestPlanAlertsEmptyActiveResolvesAll(t *testing.T) {
	p := planAlerts([]existingAlert{{ID: 9, Fingerprint: "x"}}, nil)
	if len(p.Resolve) != 1 || len(p.Insert) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestFormatAlerts(t *testing.T) {
	newA := []alertRow{{ID: 1, Severity: "critical", Title: "Backup failed: vm1", Detail: "disk full"}}
	rem := []alertRow{{ID: 2, Severity: "warning", Title: "Stale: vm2"}}
	res := []alertRow{{ID: 3, Severity: "critical", Title: "Source back: pve", Detail: "ignored for resolved"}}
	n := formatAlerts(newA, rem, res)
	for _, want := range []string{"CRITICAL", "1 new", "1 still open", "1 resolved", "[CRITICAL] Backup failed: vm1 — disk full", "[WARNING] Stale: vm2", "[OK] Source back: pve"} {
		if !strings.Contains(n.Subject+n.Text, want) {
			t.Errorf("missing %q in:\n%s", want, n.Text)
		}
	}
	if strings.Contains(n.Text, "ignored for resolved") {
		t.Error("resolved detail should not be printed")
	}
	if got := len(n.Payload["alerts"].([]map[string]any)); got != 3 {
		t.Errorf("payload alerts = %d", got)
	}
}

func TestFormatAlertsTruncates(t *testing.T) {
	var many []alertRow
	for i := 0; i < 60; i++ {
		many = append(many, alertRow{ID: int64(i), Severity: "warning", Title: "t"})
	}
	n := formatAlerts(many, nil, nil)
	if !strings.Contains(n.Text, "and 35 more") {
		t.Errorf("expected truncation note:\n%s", n.Text)
	}
	if strings.Contains(n.Subject, "CRITICAL") {
		t.Error("no critical alerts, subject must not say CRITICAL")
	}
}

func TestAlertSettingsValidate(t *testing.T) {
	ok := defaultAlertSettings()
	if m := ok.validate(); m != "" {
		t.Fatal(m)
	}
	for name, mut := range map[string]func(*alertSettings){
		"stale0":   func(s *alertSettings) { s.StaleHours = 0 },
		"storage":  func(s *alertSettings) { s.StoragePct = 100 },
		"storage2": func(s *alertSettings) { s.StoragePct = 10 },
		"repeat":   func(s *alertSettings) { s.RepeatHours = -1 },
	} {
		s := ok
		mut(&s)
		if s.validate() == "" {
			t.Errorf("%s should be rejected", name)
		}
	}
}

func TestRetentionValidate(t *testing.T) {
	if m := defaultRetention().validate(); m != "" {
		t.Fatal(m)
	}
	s := defaultRetention()
	s.AuditDays = 0 // keep forever is allowed
	if s.validate() != "" {
		t.Error("0 must mean keep forever")
	}
	s.AuditDays = 30 // below the 90-day minimum
	if s.validate() == "" {
		t.Error("audit below minimum must be rejected")
	}
	s = defaultRetention()
	s.SessionsDays = 4000
	if s.validate() == "" {
		t.Error("above max must be rejected")
	}
}
