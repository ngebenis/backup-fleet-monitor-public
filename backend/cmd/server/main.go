package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const version = "0.2.0-phase2"

func main() {
	mode := "api"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Println("DATABASE_URL required")
		os.Exit(1)
	}
	if err := loadSecretKey(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	var db, err = openDB(ctx, dbURL)
	if err != nil {
		fmt.Println("db:", err)
		os.Exit(1)
	}
	defer db.Close()
	for i := 0; i < 30; i++ {
		if err = db.Ping(ctx); err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		fmt.Println("db unreachable:", err)
		os.Exit(1)
	}
	switch mode {
	case "api":
		if err := migrate(ctx, db); err != nil {
			fmt.Println("migrate:", err)
			os.Exit(1)
		}
		if err := syncRolePermissions(ctx, db); err != nil {
			fmt.Println("roles:", err)
			os.Exit(1)
		}
		bootstrapAdmin(ctx, db)
		ttl := 12 * time.Hour
		if d, err := time.ParseDuration(os.Getenv("SESSION_TTL")); err == nil && d > 0 {
			ttl = d
		}
		app := &App{db: db, cookieSecure: os.Getenv("COOKIE_SECURE") == "true", sessionTTL: ttl, loginLimit: newLimiter(10, time.Minute)}
		srv := &http.Server{Addr: ":8080", Handler: app.routes(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
		go func() { <-ctx.Done(); c, cl := context.WithTimeout(context.Background(), 5*time.Second); defer cl(); srv.Shutdown(c) }()
		fmt.Println("api listening :8080", version)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			fmt.Println(err)
			os.Exit(1)
		}
	case "poller":
		runPoller(ctx, db)
	default:
		fmt.Println("unknown mode", mode)
		os.Exit(2)
	}
}

