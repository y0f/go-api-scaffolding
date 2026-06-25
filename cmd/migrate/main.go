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
	"fmt"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/y0f/go-api-scaffolding/internal/config"
	"github.com/y0f/go-api-scaffolding/migrations"
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

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := sql.Open("pgx", cfg.Database.URL)
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
