package widget_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/y0f/gophling/v2/migrations"
)

// widgets already holds rows when a deployment upgrades, so the list-index
// migration must not lock the table for the length of an index build: it runs
// outside a transaction and builds and drops indexes CONCURRENTLY.
func TestListIndexMigrationDoesNotBlockWrites(t *testing.T) {
	// forge init renumbers migrations when it drops a slice, so match the name
	// after the version prefix.
	names, err := fs.Glob(migrations.FS, "*_widgets_list_index.sql")
	if err != nil || len(names) != 1 {
		t.Fatalf("list-index migration: found %v (%v), want exactly one", names, err)
	}
	body, err := migrations.FS.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	if !strings.Contains(src, "-- +goose NO TRANSACTION") {
		t.Error("migration runs in a transaction, which holds its index locks until commit")
	}
	stmts := regexp.MustCompile(`(?im)^\s*(CREATE|DROP)\s+INDEX\b.*$`).FindAllString(src, -1)
	if len(stmts) == 0 {
		t.Fatal("no index statements found")
	}
	for _, stmt := range stmts {
		if !strings.Contains(strings.ToUpper(stmt), "CONCURRENTLY") {
			t.Errorf("index statement takes a blocking lock: %s", strings.TrimSpace(stmt))
		}
	}
}
