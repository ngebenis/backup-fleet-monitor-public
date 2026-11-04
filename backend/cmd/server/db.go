package main

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migFS embed.FS

func openDB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 8
	return pgxpool.NewWithConfig(ctx, cfg)
}

func migrate(ctx context.Context, db *pgxpool.Pool) error {
	conn, err := db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(727001)"); err != nil {
		return err
	}
	defer conn.Exec(ctx, "SELECT pg_advisory_unlock(727001)")
	if _, err := conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return err
	}
	ents, _ := migFS.ReadDir("migrations")
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)", n).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		b, _ := migFS.ReadFile("migrations/" + n)
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(b)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", n, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES($1)", n); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		fmt.Println("applied migration", strings.TrimSuffix(n, ".sql"))
	}
	return nil
}

var rolePerms = map[string][]string{
	"super_admin": {"dashboard.read", "sources.read", "sources.create", "sources.update", "sources.test", "sources.enable", "sources.disable", "sources.delete",
		"jobs.read", "jobs.export", "alerts.read", "alerts.acknowledge", "alerts.resolve", "alerts.configure", "reports.read", "reports.export",
		"users.read", "users.create", "users.update", "users.disable", "users.delete", "roles.read", "roles.manage",
		"settings.read", "settings.update", "audit.read", "audit.export", "config.backup", "config.restore"},
	"administrator": {"dashboard.read", "sources.read", "sources.create", "sources.update", "sources.test", "sources.enable", "sources.disable",
		"jobs.read", "jobs.export", "alerts.read", "alerts.acknowledge", "alerts.resolve", "alerts.configure", "reports.read", "reports.export",
		"settings.read", "settings.update", "roles.read", "users.read"},
	"backup_operator": {"dashboard.read", "sources.read", "jobs.read", "jobs.export", "alerts.read", "alerts.acknowledge", "reports.read", "reports.export"},
	"auditor":         {"dashboard.read", "sources.read", "jobs.read", "alerts.read", "reports.read", "audit.read", "users.read", "roles.read"},
}

// syncRolePermissions makes system role permissions match code (deny by default).
func syncRolePermissions(ctx context.Context, db *pgxpool.Pool) error {
	for role, perms := range rolePerms {
		var id int
		if err := db.QueryRow(ctx, "SELECT id FROM roles WHERE name=$1", role).Scan(&id); err != nil {
			return err
		}
		if _, err := db.Exec(ctx, "DELETE FROM role_permissions WHERE role_id=$1", id); err != nil {
			return err
		}
		for _, p := range perms {
			if _, err := db.Exec(ctx, "INSERT INTO role_permissions(role_id,permission) VALUES($1,$2)", id, p); err != nil {
				return err
			}
		}
	}
	return nil
}
