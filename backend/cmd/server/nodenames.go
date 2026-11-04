package main

import (
	"context"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// nodeResolver maps the raw identifier an adapter reports (IP, Veeam GUID,
// hostname) to the FQDN the operators want in the Node column.
//
// The map lives in the node_aliases table so it can be corrected with a plain
// UPDATE instead of a rebuild. It is cached in memory, refreshed periodically,
// and backed by reverse DNS. An unknown host still renders its raw value, so
// nothing is silently blank or hardcoded.
type nodeResolver struct {
	mu  sync.RWMutex
	tbl map[string]string
	at  time.Time
}

const nodeAliasTTL = 5 * time.Minute

var nodes = &nodeResolver{tbl: map[string]string{}}

// refresh reloads node_aliases. A failure keeps the previous map in place.
func (n *nodeResolver) refresh(ctx context.Context, db *pgxpool.Pool) {
	if db == nil {
		return
	}
	rows, err := db.Query(ctx, `SELECT raw, alias FROM node_aliases`)
	if err != nil {
		return
	}
	defer rows.Close()
	m := make(map[string]string, 32)
	for rows.Next() {
		var raw, alias string
		if err := rows.Scan(&raw, &alias); err != nil {
			continue
		}
		m[strings.ToUpper(strings.TrimSpace(raw))] = alias
	}
	if rows.Err() != nil || len(m) == 0 {
		return
	}
	n.mu.Lock()
	n.tbl = m
	n.at = time.Now()
	n.mu.Unlock()
}

// refreshIfStale reloads the table at most once per TTL.
func (n *nodeResolver) refreshIfStale(ctx context.Context, db *pgxpool.Pool) {
	n.mu.RLock()
	fresh := time.Since(n.at) < nodeAliasTTL
	n.mu.RUnlock()
	if !fresh {
		n.refresh(ctx, db)
	}
}

// name resolves a raw node identifier to its display name.
func (n *nodeResolver) name(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	n.mu.RLock()
	v, ok := n.tbl[strings.ToUpper(raw)]
	n.mu.RUnlock()
	if ok {
		return v
	}

	// Not listed: try reverse DNS once, then show the raw value as-is.
	if net.ParseIP(raw) != nil {
		if names, err := net.LookupAddr(raw); err == nil && len(names) > 0 {
			return strings.TrimSuffix(names[0], ".")
		}
	}
	return raw
}

// sourceHost extracts the hostname from a source base_url, dropping any port.
// Used by adapters that report no per-object node (Synology ABB).
func (n *nodeResolver) sourceHost(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return n.name(u.Hostname())
}

// nodeName is the package-level shorthand used by the adapters.
func nodeName(raw string) string { return nodes.name(raw) }

// sourceHostOf is the package-level shorthand for sourceHost.
func sourceHostOf(baseURL string) string { return nodes.sourceHost(baseURL) }
