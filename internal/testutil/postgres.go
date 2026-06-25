//go:build integration

package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
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
	dbCounter atomic.Int64
)

// start launches one Postgres container per test binary, applies migrations to
// a template database, and records the admin DSN. Each test then clones the
// template into its own database, which makes the integration tests safe to run
// in parallel at full speed.
func start(ctx context.Context) {
	container, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("forge"),
		postgres.WithPassword("forge"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		startErr = fmt.Errorf("start postgres container: %w", err)
		return
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		startErr = fmt.Errorf("connection string: %w", err)
		return
	}
	baseDSN = dsn

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		startErr = err
		return
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+templateDB); err != nil {
		startErr = fmt.Errorf("create template database: %w", err)
		return
	}

	templateConn, err := sql.Open("pgx", withDatabase(dsn, templateDB))
	if err != nil {
		startErr = err
		return
	}
	defer templateConn.Close()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		startErr = err
		return
	}
	if err := goose.UpContext(ctx, templateConn, "."); err != nil {
		startErr = fmt.Errorf("migrate template database: %w", err)
	}
}

// NewDB returns a connection pool to a fresh database cloned from the migrated
// template. The database is dropped when the test finishes.
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	startOnce.Do(func() { start(ctx) })
	if startErr != nil {
		t.Fatalf("postgres setup: %v", startErr)
	}

	name := fmt.Sprintf("test_%d", dbCounter.Add(1))
	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, templateDB)); err != nil {
		admin.Close()
		t.Fatalf("clone template database: %v", err)
	}
	admin.Close()

	pool, err := database.NewPool(ctx, database.Config{URL: withDatabase(baseDSN, name)})
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, err := sql.Open("pgx", baseDSN)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name))
	})
	return pool
}

func withDatabase(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + name
	return u.String()
}
