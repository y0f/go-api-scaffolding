//go:build integration

package testutil

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

func TestExternalPostgresIsolationAndCleanup(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("FORGE_TEST_DATABASE_URL")
	if dsn == "" {
		startOnce.Do(func() { start(ctx) })
		if startErr != nil {
			t.Fatal(startErr)
		}
		dsn = baseDSN
		t.Setenv("FORGE_TEST_DATABASE_URL", dsn)
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var names []string
	t.Run("isolated databases", func(t *testing.T) {
		first, second := NewDB(t), NewDB(t)
		var firstName, secondName string
		if err := first.QueryRow(ctx, "SELECT current_database()").Scan(&firstName); err != nil {
			t.Fatal(err)
		}
		if err := second.QueryRow(ctx, "SELECT current_database()").Scan(&secondName); err != nil {
			t.Fatal(err)
		}
		names = []string{firstName, secondName}
		if firstName == secondName {
			t.Fatal("tests shared a database")
		}
		if _, err := first.Exec(ctx, "CREATE TABLE isolation_probe (id integer)"); err != nil {
			t.Fatal(err)
		}
		var exists bool
		if err := second.QueryRow(ctx, "SELECT to_regclass('public.isolation_probe') IS NOT NULL").Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatal("one test's schema leaked into another")
		}
	})
	for _, name := range names {
		var count int
		if err := admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_database WHERE datname = $1", name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("test database %s was not removed", name)
		}
	}
}
