package main

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceDomainFilenamesDoNotCollideOrSelectPlatforms(t *testing.T) {
	for _, name := range []string{"Store", "Service", "Idempotent", "ServiceTest", "AuditTest", "InvoiceLinux", "InvoiceWindows", "InvoiceAMD64", "OrderItem"} {
		t.Run(name, func(t *testing.T) {
			resourceFixture(t)
			res := newResource(name)
			//forge:begin idempotency
			res.Idempotency = true
			//forge:end idempotency
			if err := addResource(res, false); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join("internal", "modules", res.Snake)
			filename := resourceDomainFile(res)
			if strings.HasSuffix(filename, "_test.go") {
				t.Fatalf("domain file is a test-only source: %s", filename)
			}
			for _, platform := range []struct{ os, arch string }{{"linux", "amd64"}, {"windows", "arm64"}, {"darwin", "arm64"}} {
				context := build.Context{GOOS: platform.os, GOARCH: platform.arch}
				match, err := context.MatchFile(dir, filename)
				if err != nil || !match {
					t.Fatalf("domain %s excluded on %s/%s: %v", filename, platform.os, platform.arch, err)
				}
			}
		})
	}
}

func TestResourceRejectsPendingTableCollisionsBeforeWriting(t *testing.T) {
	for _, fixture := range []struct{ resource, table string }{{"Order", "orders"}, {"OutboxMessage", "outbox_messages"}, {"IdempotencyKey", "idempotency_keys"}} {
		t.Run(fixture.resource, func(t *testing.T) {
			resourceFixture(t)
			source := "-- +goose Up\nCREATE TABLE public.\"" + fixture.table + "\" (id uuid);\n-- +goose Down\nDROP TABLE " + fixture.table + ";\n"
			if err := os.WriteFile("migrations/00001_existing.sql", []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, dryRun := range []bool{true, false} {
				err := addResource(newResource(fixture.resource), dryRun)
				if err == nil || !strings.Contains(err.Error(), "already exists in migration sources") {
					t.Fatalf("table collision accepted (dry-run=%v): %v", dryRun, err)
				}
			}
			if _, err := os.Stat("internal"); !os.IsNotExist(err) {
				t.Fatalf("source written before table collision rejection: %v", err)
			}
		})
	}
}

func TestMigrationTableCheckUsesUpOrderAndIgnoresCommentsAndBodies(t *testing.T) {
	resourceFixture(t)
	files := map[string]string{
		"00001_initial.sql": `-- +goose Up
/* CREATE TABLE orders(id int); /* nested comment */ */
CREATE TABLE old_notes (id int);
CREATE TABLE renamed (id int);
CREATE TABLE audit.orders (id int);
CREATE FUNCTION example() RETURNS text AS $body$
BEGIN; CREATE TABLE orders(id int); END;
$body$ LANGUAGE plpgsql;
SELECT 'CREATE TABLE orders(id int);';
-- +goose Down
DROP TABLE old_notes, renamed;
`,
		"00002_rename.sql": "ALTER TABLE public.renamed RENAME TO labels; DROP TABLE IF EXISTS old_notes;",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join("migrations", name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"orders", "old_notes", "renamed"} {
		if err := validateResourceTable(name); err != nil {
			t.Fatalf("unused public table %s rejected: %v", name, err)
		}
	}
	if err := validateResourceTable("labels"); err == nil {
		t.Fatal("renamed table was not detected")
	}
}

func TestOptionalPackagesIgnoreRetainedNotesAndTestFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{"outbox", "idempotency"} {
		dir := filepath.Join("internal", name)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		for _, filename := range []string{"notes.txt", "local_test.go", "_ignored.go", ".ignored.go"} {
			if err := os.WriteFile(filepath.Join(dir, filename), []byte("local notes"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if res := newResource("Note"); res.Outbox || res.Idempotency {
		t.Fatalf("retained non-package directories enabled optional components: %#v", res)
	}
	for _, name := range []string{"outbox", "idempotency"} {
		if err := os.WriteFile(filepath.Join("internal", name, "implementation.go"), []byte("package "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if res := newResource("Note"); !res.Outbox || !res.Idempotency {
		t.Fatal("existing package implementations were not detected")
	}
}

func TestResourceRejectsFixedSQLCNamesWithoutGeneratedDatabase(t *testing.T) {
	for _, name := range []string{"Querier", "Queries", "DBTX", "New"} {
		t.Run(name, func(t *testing.T) {
			resourceFixture(t)
			if err := addResource(newResource(name), false); err == nil || !strings.Contains(err.Error(), "sqlc declaration") {
				t.Fatalf("fixed sqlc declaration accepted: %v", err)
			}
			if _, err := os.Stat("internal"); !os.IsNotExist(err) {
				t.Fatalf("source written before declaration rejection: %v", err)
			}
		})
	}
}

func TestResourceRejectsSQLCModelsQueryTypesAndMethods(t *testing.T) {
	for _, fixture := range []struct{ name, source string }{
		{"Invoice", "type Invoice struct{}"},
		{"ListWidgetsParams", "type ListWidgetsParams struct{}"},
		{"GetWidgetRow", "type GetWidgetRow struct{}"},
		{"Invoice", "type UpdateInvoiceParams struct{}"},
		{"Invoice", "type GetInvoiceRow struct{}"},
		{"Invoice", "type Queries struct{}; func (*Queries) CreateInvoice() {}"},
	} {
		t.Run(fixture.name+fixture.source, func(t *testing.T) {
			resourceFixture(t)
			if err := os.MkdirAll("internal/gen/db", 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("internal/gen/db/queries.sql.go", []byte("package db\n"+fixture.source), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := addResource(newResource(fixture.name), false); err == nil || !strings.Contains(err.Error(), "sqlc") {
				t.Fatalf("generated sqlc collision accepted: %v", err)
			}
			if _, err := os.Stat("internal/modules"); !os.IsNotExist(err) {
				t.Fatalf("source written before declaration rejection: %v", err)
			}
		})
	}
}
