package main

import (
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/y0f/gophling/v2/migrations"
)

// migrate only needs FORGE_DB_URL, so unrelated API settings (auth in
// production, the admin token) must not stop it before it reaches the database.
func TestMigrateIgnoresUnrelatedConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"production without auth", map[string]string{"FORGE_ENV": "production"}},
		{"admin enabled without token", map[string]string{"FORGE_ADMIN_ENABLED": "true"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FORGE_DB_URL", "postgres://x:y@127.0.0.1:1/db")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			old := os.Args
			os.Args = []string{"migrate", "status"}
			defer func() { os.Args = old }()
			err := run()
			if err != nil && !strings.Contains(err.Error(), "run \"status\"") {
				t.Fatalf("migrate failed on non-database config before connecting: %v", err)
			}
		})
	}
}

// DatabaseConfig defaults the URL to the development database, so without
// this check a migration job missing FORGE_DB_URL would migrate localhost.
func TestMigrateRequiresDatabaseURL(t *testing.T) {
	for _, value := range []string{"unset", ""} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("FORGE_DB_URL", "")
			if value == "unset" {
				_ = os.Unsetenv("FORGE_DB_URL")
			}
			old := os.Args
			os.Args = []string{"migrate", "status"}
			defer func() { os.Args = old }()
			err := run()
			if err == nil || err.Error() != "FORGE_DB_URL is required" {
				t.Fatalf("err = %v, want FORGE_DB_URL is required", err)
			}
		})
	}
}

// identPart is one identifier part: bare, or double-quoted with "" as an
// escaped quote (a quoted part may contain dots).
const identPart = `(?:"(?:[^"]|"")+"|\w+)`

// ident matches a possibly schema-qualified identifier.
const ident = identPart + `(?:\.` + identPart + `)?`

var (
	// goose matches its annotations case-insensitively.
	noTxRe        = regexp.MustCompile(`(?mi)^--\s*\+goose NO TRANSACTION\s*$`)
	identPartRe   = regexp.MustCompile(identPart)
	sqlCommentRe  = regexp.MustCompile(`--[^\n]*`)
	createTableRe = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(` + ident + `)`)
	createIndexRe = regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+(CONCURRENTLY\s+)?(?:(?:IF\s+NOT\s+EXISTS\s+)?(` + ident + `)\s*)??\bON\s+(?:ONLY\s+)?(` + ident + `)`)
	dropIndexRe   = regexp.MustCompile(`(?i)\bDROP\s+INDEX\s+(CONCURRENTLY\s+)?(?:IF\s+EXISTS\s+)?(` + ident + `)`)
)

// normIdent folds an identifier the way Postgres does: bare parts to lower
// case, quoted parts kept as written without their quotes.
func normIdent(s string) string {
	parts := identPartRe.FindAllString(s, -1)
	for i, p := range parts {
		if strings.HasPrefix(p, `"`) {
			parts[i] = strings.ReplaceAll(p[1:len(p)-1], `""`, `"`)
		} else {
			parts[i] = strings.ToLower(p)
		}
	}
	return strings.Join(parts, ".")
}

// releasedMigrations already ran on deployed databases and cannot change, so
// the guard below leaves them alone. They are keyed by the name after the
// version prefix, which forge init renumbers when it drops a slice.
var releasedMigrations = map[string]bool{
	"widgets.sql":     true,
	"outbox.sql":      true,
	"idempotency.sql": true,
}

// A plain CREATE INDEX holds a lock that blocks writes to the table for the
// whole build, and a plain DROP INDEX takes an exclusive lock that queues
// every query on the table behind it, so an index on a table an earlier
// migration created must be built and dropped concurrently, which goose can
// only run outside a transaction.
func TestMigrationsBuildIndexesOnExistingTablesConcurrently(t *testing.T) {
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, rest, _ := strings.Cut(name, "_"); releasedMigrations[rest] {
			continue
		}
		src, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range blockingIndexChanges(string(src)) {
			t.Errorf("%s: %s", name, problem)
		}
	}
}

// blockingIndexChanges lists the index builds and drops in a migration that
// lock a table some earlier migration created. Indexes on tables the
// migration creates itself are exempt: nothing else uses those yet.
func blockingIndexChanges(src string) []string {
	noTx := noTxRe.MatchString(src)
	sql := sqlCommentRe.ReplaceAllString(src, "")
	created := map[string]bool{}
	for _, m := range createTableRe.FindAllStringSubmatch(sql, -1) {
		created[normIdent(m[1])] = true
	}
	var problems []string
	ownIndexes := map[string]bool{}
	for _, m := range createIndexRe.FindAllStringSubmatch(sql, -1) {
		if created[normIdent(m[3])] {
			ownIndexes[normIdent(m[2])] = true
			continue
		}
		if m[1] == "" || !noTx {
			problems = append(problems, fmt.Sprintf("index on existing table %s must use CREATE INDEX CONCURRENTLY under -- +goose NO TRANSACTION", m[3]))
		}
	}
	for _, m := range dropIndexRe.FindAllStringSubmatch(sql, -1) {
		if ownIndexes[normIdent(m[2])] {
			continue
		}
		if m[1] == "" || !noTx {
			problems = append(problems, fmt.Sprintf("index %s must be dropped with DROP INDEX CONCURRENTLY under -- +goose NO TRANSACTION", m[2]))
		}
	}
	return problems
}

func TestBlockingIndexChanges(t *testing.T) {
	const noTx = "-- +goose NO TRANSACTION\n"
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"concurrent build and drop", noTx + "-- +goose Up\nDROP INDEX CONCURRENTLY IF EXISTS w_idx;\nCREATE INDEX CONCURRENTLY w_idx ON widgets (name);\n", 0},
		{"plain named build", "-- +goose Up\nCREATE INDEX w_idx ON widgets (name);\n", 1},
		{"plain unnamed build", noTx + "-- +goose Up\nCREATE INDEX ON widgets (name);\n", 1},
		{"concurrent unnamed build", noTx + "-- +goose Up\nCREATE UNIQUE INDEX CONCURRENTLY ON widgets (name);\n", 0},
		{"plain drop", noTx + "-- +goose Up\nDROP INDEX IF EXISTS w_idx;\n", 1},
		{"concurrent drop inside a transaction", "-- +goose Up\nDROP INDEX CONCURRENTLY w_idx;\n", 1},
		{"plain drop in down", noTx + "-- +goose Up\nSELECT 1;\n-- +goose Down\nDROP INDEX w_idx;\n", 1},
		{"own table", "-- +goose Up\nCREATE TABLE t (id INT);\nCREATE INDEX t_idx ON t (id);\nCREATE INDEX ON t (id);\n-- +goose Down\nDROP INDEX t_idx;\nDROP TABLE t;\n", 0},
		{"commented out", "-- +goose Up\n-- CREATE INDEX ON widgets (name); DROP INDEX w_idx;\nSELECT 1;\n", 0},
		{"quoted named build", "-- +goose Up\nCREATE INDEX \"w_idx\" ON \"widgets\" (name);\n", 1},
		{"quoted unnamed build", noTx + "-- +goose Up\nCREATE INDEX ON \"widgets\" (name);\n", 1},
		{"quoted schema-qualified unnamed build", "-- +goose Up\nCREATE INDEX ON public.\"widgets\"(name);\n", 1},
		{"quoted concurrent build", noTx + "-- +goose Up\nCREATE INDEX CONCURRENTLY \"w_idx\" ON \"widgets\" (name);\n", 0},
		{"quoted plain drop", noTx + "-- +goose Up\nDROP INDEX \"w_idx\";\n", 1},
		{"quoted concurrent drop", noTx + "-- +goose Up\nDROP INDEX CONCURRENTLY IF EXISTS \"w_idx\";\n", 0},
		{"quoted own table", "-- +goose Up\nCREATE TABLE \"T\" (id INT);\nCREATE INDEX \"T_idx\" ON \"T\" (id);\nCREATE INDEX ON \"T\" (id);\n-- +goose Down\nDROP INDEX \"T_idx\";\n", 0},
		{"bare name is not the quoted own table", "-- +goose Up\nCREATE TABLE \"T\" (id INT);\nCREATE INDEX ON T (id);\n", 1},
		{"annotation inside a comment", "-- +goose Up\n-- not -- +goose NO TRANSACTION\nCREATE INDEX CONCURRENTLY w_idx ON widgets (name);\n", 1},
		{"annotation with trailing text", "-- +goose NO TRANSACTION please\n-- +goose Up\nDROP INDEX CONCURRENTLY w_idx;\n", 1},
		{"annotation without space", "--+goose NO TRANSACTION\n-- +goose Up\nDROP INDEX CONCURRENTLY w_idx;\n", 0},
		{"lower-case annotation", "-- +goose no transaction\n-- +goose Up\nDROP INDEX CONCURRENTLY w_idx;\n", 0},
		{"quoted own table with a dot", "-- +goose Up\nCREATE TABLE \"a.b\" (id INT);\nCREATE INDEX ON \"a.b\" (id);\n", 0},
		{"quoted name with a dot is not schema-qualified", "-- +goose Up\nCREATE TABLE b (id INT);\nCREATE INDEX ON \"a.b\" (id);\n", 1},
		{"escaped quote in own table", "-- +goose Up\nCREATE TABLE \"a\"\"b\" (id INT);\nCREATE INDEX ON \"a\"\"b\" (id);\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blockingIndexChanges(tc.src); len(got) != tc.want {
				t.Errorf("got %d problems %q, want %d", len(got), got, tc.want)
			}
		})
	}
}
