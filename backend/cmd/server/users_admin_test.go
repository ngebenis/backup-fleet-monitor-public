package main

import (
	"strings"
	"testing"
)

func TestValidNewPassword(t *testing.T) {
	cases := []struct {
		pw, user string
		ok       bool
	}{
		{"correct horse battery", "alice", true},
		{"short", "alice", false},
		{"aaaaaaaaaaaaaaaa", "alice", false},
		{"alice-alice-alice", "alice-alice-alice", false}, // equals username
		{"ALICE-ALICE-ALICE", "alice-alice-alice", false}, // case-insensitive
		{strings.Repeat("ab", 70), "alice", false},        // > 128
		{"pässwörd-123456", "alice", true},                // counted in runes, not bytes
	}
	for _, c := range cases {
		if got := validNewPassword(c.pw, c.user) == ""; got != c.ok {
			t.Errorf("%q: ok=%v want %v", c.pw, got, c.ok)
		}
	}
}

func TestTempPassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := tempPassword()
		if err != nil || len(p) != 16 {
			t.Fatalf("%q %v", p, err)
		}
		for _, c := range p {
			if !strings.ContainsRune(tempPwAlphabet, c) {
				t.Fatalf("char %q outside alphabet", c)
			}
		}
		if validNewPassword(p, "x") != "" {
			t.Fatalf("generated password fails own policy: %q", p)
		}
		seen[p] = true
	}
	if len(seen) < 50 {
		t.Error("passwords repeat")
	}
}

func TestUsernameRe(t *testing.T) {
	for u, ok := range map[string]bool{"admin": true, "a.b-c_d": true, "ab": false, "-abc": false, "Abc": false, "a b c": false, "a@b.c": false, strings.Repeat("a", 65): false} {
		if usernameRe.MatchString(u) != ok {
			t.Errorf("%q want %v", u, ok)
		}
	}
}
