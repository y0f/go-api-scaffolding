//go:build integration

package testutil

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // Register database/sql support for goose migrations.
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/y0f/go-api-scaffolding/internal/platform/database"
	"github.com/y0f/go-api-scaffolding/migrations"
)

const templateDB = "forge_template"

var (
	startOnce sync.Once
	baseDSN   string
	startErr  error
)

// start launches one Postgres container per test binary and migrates a template
// database. Testcontainers removes the container when the test process exits.
func start(ctx context.Context) {
	container, err := postgres.Run(ctx, "postgres:18-alpine@sha256:123b17998ccaf47e53b502e8c6a1e79afd8f579c6f3eec83c563da026f957395",
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("forge"),
		postgres.WithPassword("forge"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		startErr = fmt.Errorf("start postgres container: %w", err)
		return
	}
	baseDSN, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		startErr = fmt.Errorf("connection string: %w", err)
		return
	}
	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		startErr = err
		return
	}
	defer func() { _ = admin.Close() }()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+templateDB); err != nil {
		startErr = fmt.Errorf("create template database: %w", err)
		return
	}
	startErr = migrateDatabase(ctx, baseDSN, templateDB)
}

func migrateDatabase(ctx context.Context, dsn, name string) error {
	dsn, err := databaseURL(dsn, name)
	if err != nil {
		return err
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	// A separate provider avoids goose's package-global mutable configuration,
	// so external databases can be migrated concurrently by parallel tests.
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, migrations.FS)
	if errors.Is(err, goose.ErrNoMigrations) {
		return nil // A minimal initialized project has no migrations yet.
	}
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate test database: %w", err)
	}
	return nil
}

// NewDB returns an isolated migrated database and drops it when the test ends.
// FORGE_TEST_DATABASE_URL selects an existing Postgres server instead of Docker.
// It must name a dedicated test server/user with CREATEDB permission. External
// databases are migrated individually so no shared template is left behind.
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dsn := os.Getenv("FORGE_TEST_DATABASE_URL")
	external := dsn != ""
	if external {
		if _, err := databaseURL(dsn, "postgres"); err != nil {
			t.Fatalf("FORGE_TEST_DATABASE_URL: %v", err)
		}
	} else {
		startOnce.Do(func() { start(ctx) })
		if startErr != nil {
			t.Fatalf("postgres setup: %v", startErr)
		}
		dsn = baseDSN
	}
	name := "forge_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("open test admin connection: invalid database configuration")
	}
	defer func() { _ = admin.Close() }()
	statement := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize() //#nosec G202 -- generated UUID identifier, quoted by pgx
	if !external {
		statement += " TEMPLATE " + templateDB
	}
	if _, err := admin.ExecContext(ctx, statement); err != nil {
		t.Fatalf("create isolated test database (check connectivity and CREATEDB permission): %v", err)
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		cleanup, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Error("open cleanup connection: invalid database configuration")
			return
		}
		defer func() { _ = cleanup.Close() }()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := cleanup.ExecContext(cleanupCtx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("drop isolated test database %s: %v", name, err)
		}
	})
	if external {
		if err := migrateDatabase(ctx, dsn, name); err != nil {
			t.Fatal(err)
		}
	}
	testDSN, err := databaseURL(dsn, name)
	if err != nil {
		t.Fatal(err)
	}
	pool, err = database.NewPool(ctx, database.Config{URL: testDSN})
	if err != nil {
		t.Fatalf("connect to isolated test database: %v", err)
	}
	return pool
}
