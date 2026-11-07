// Command migrate applies database migrations as an explicit deploy step.
// Migrations are never run automatically at service startup.
//
// Usage:
//
//	migrate up        # apply all pending migrations
//	migrate down      # roll back the most recent migration
//	migrate status    # show applied and pending migrations
//	migrate version   # print the current schema version
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/caarlos0/env/v11"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/y0f/gophling/internal/config"
	"github.com/y0f/gophling/migrations"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("migrate: %v", err)
	}
}

func run() error {
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case "up", "down", "status", "version":
	default:
		return fmt.Errorf("unknown command %q (want up, down, status or version)", command)
	}

	// Only the database settings are read: a migration job must not need the
	// API's auth or admin secrets just to pass the full config validation.
	var dbCfg config.DatabaseConfig
	if err := env.ParseWithOptions(&dbCfg, env.Options{Prefix: "FORGE_DB_"}); err != nil {
		return fmt.Errorf("parse environment: %w", err)
	}
	// DatabaseConfig defaults the URL to the local development database, so
	// the field is never empty; check the variable itself, or a migration job
	// deployed without it would quietly migrate localhost instead of failing.
	if os.Getenv("FORGE_DB_URL") == "" {
		return errors.New("FORGE_DB_URL is required")
	}

	db, err := sql.Open("pgx", dbCfg.URL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.RunContext(context.Background(), command, db, "."); err != nil {
		return fmt.Errorf("run %q: %w", command, err)
	}
	return nil
}
