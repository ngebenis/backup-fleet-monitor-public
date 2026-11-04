package main

import (
	"encoding/json"
	"strings"
)

// Small helpers for ABB/DSM JSON payloads (map[string]any based).
func jstr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func jnum(m map[string]any, k string) float64 {
	if v, ok := m[k].(float64); ok {
		return v
	}
	return 0
}

func jmap(m map[string]any, k string) map[string]any {
	if v, ok := m[k].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func synoRedact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "password") || strings.Contains(lk, "passwd") || strings.Contains(lk, "token") || strings.Contains(lk, "secret") {
				t[k] = "<redacted>"
				continue
			}
			t[k] = synoRedact(x)
		}
	case []any:
		for i := range t {
			t[i] = synoRedact(t[i])
		}
	}
	return v
}
