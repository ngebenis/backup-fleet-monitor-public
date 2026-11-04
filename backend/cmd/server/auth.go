package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	aM = 64 * 1024
	aT = 3
	aP = 2
)

func hashPassword(p string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	key := argon2.IDKey([]byte(p), salt, aT, aM, aP, 32)
	e := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", aM, aT, aP, e.EncodeToString(salt), e.EncodeToString(key))
}

func verifyPassword(p, enc string) bool {
	parts := strings.Split(enc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	e := base64.RawStdEncoding
	salt, err1 := e.DecodeString(parts[4])
	want, err2 := e.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(p), salt, aT, aM, aP, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func newToken() (token, hash string) {
	b := make([]byte, 32)
	rand.Read(b)
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, tokenHash(token)
}

func tokenHash(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

var dummyHash = hashPassword("dummy-password-for-timing")

// sliding-window limiter that counts failed attempts only
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
	max  int
	win  time.Duration
}

func newLimiter(max int, win time.Duration) *limiter {
	return &limiter{hits: map[string][]time.Time{}, max: max, win: win}
}

// allow reports whether key is still under the limit, without recording a hit.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, time.Now())) < l.max
}

// fail records one failed attempt for key.
func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.hits[key] = append(l.prune(key, now), now)
	// Opportunistic cleanup so the map cannot grow without bound.
	if len(l.hits) > 10000 {
		for k := range l.hits {
			if len(l.prune(k, now)) == 0 {
				delete(l.hits, k)
			}
		}
	}
}

// reset forgets key (used after a successful login).
func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}

func (l *limiter) prune(key string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.win {
			keep = append(keep, t)
		}
	}
	l.hits[key] = keep
	return keep
}
