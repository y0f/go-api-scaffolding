package main

import (
	"go/format"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLConfigPreservesExplicitModelNames(t *testing.T) {
	src := "version: \"2\"\nsql:\n  - engine: postgresql\n    queries:\n      - internal/outbox/queries.sql\n    gen:\n      go:\n        out: internal/gen/db\n"
	for _, name := range []string{"Quiz", "News"} {
		var err error
		src, err = addSQLConfig(src, newResource(name))
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"quizes: Quiz", "newses: News", "- quizes", "- newses", "- internal/modules/quiz/queries.sql", "- internal/outbox/queries.sql"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q in %s", want, src)
		}
	}
	if _, err := addSQLConfig("version: \"2\"\n", newResource("Order")); err == nil {
		t.Fatal("invalid sqlc config accepted")
	}
}

// Generated imports must use the module of the project forge runs in, not the
// scaffold's, so a prebuilt forge works in an initialised project.
func TestNewResourceReadsModuleFromGoMod(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if got := newResource("Order").Module; got != moduleName {
		t.Errorf("without go.mod Module = %q, want fallback %q", got, moduleName)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("// app\nmodule github.com/example/app // comment\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := newResource("Order").Module; got != "github.com/example/app" {
		t.Errorf("Module = %q, want github.com/example/app", got)
	}
}

func TestNextStepsDescribeContractWorkflow(t *testing.T) {
	out := captureStdout(t, func() { printNextSteps(newResource("Invoice")) })
	for _, want := range []string{"api/openapi.yaml", "cmd/api/resources.go", "invoices:write", "go run ./cmd/dev generate", "go run ./cmd/dev check"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in next steps: %s", want, out)
		}
	}
	if strings.Contains(out, "Mounts") {
		t.Fatal("default workflow must use the contract")
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	f()
	w.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func resourceFixture(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"api/openapi.yaml": `openapi: 3.0.3
info: {title: Test, version: 1.0.0}
paths:
components:
  securitySchemes:
    bearerAuth: {type: http, scheme: bearer}
  parameters:
    Limit:
      name: limit
      in: query
      schema: {type: integer, format: int32}
    Offset:
      name: offset
      in: query
      schema: {type: integer, format: int32}
  responses:
    Problem: {description: Problem}
  schemas:
    Pagination: {type: object}
`,
		"cmd/api/resources.go": `package main
import (
    api "example.com/test/internal/gen/api"
    "github.com/jackc/pgx/v5/pgxpool"
    // forge:resource-imports
)
type applicationAPI struct {
    // forge:resource-fields
}
func newApplicationAPI(pool *pgxpool.Pool) *applicationAPI {
    app := &applicationAPI{}
    // forge:resource-wiring
    return app
}
`,
		"sqlc.yaml": "version: \"2\"\nsql:\n  - engine: postgresql\n    queries:\n    gen:\n      go:\n        out: internal/gen/db\n",
	}
	for path, src := range files {
		dst := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "migrations"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

func TestAddResourcesUpdatesContractAndComposition(t *testing.T) {
	resourceFixture(t)
	for _, name := range []string{"Order", "Category"} {
		if err := addResource(newResource(name), false); err != nil {
			t.Fatal(err)
		}
	}
	contract, _ := os.ReadFile("api/openapi.yaml")
	for _, path := range []string{"/v1/orders:", "/v1/categories:", "operationId: updateCategory"} {
		if !strings.Contains(string(contract), path) {
			t.Errorf("missing contract entry %s", path)
		}
	}
	wiring, _ := os.ReadFile("cmd/api/resources.go")
	for _, method := range []string{"a.order.ListOrders(w, r, params)", "a.category.UpdateCategory(w, r, id)"} {
		if !strings.Contains(string(wiring), method) {
			t.Errorf("missing composition %s", method)
		}
	}
	before := string(contract)
	if err := addResource(newResource("Order"), false); err == nil {
		t.Fatal("duplicate resource accepted")
	}
	contract, _ = os.ReadFile("api/openapi.yaml")
	if string(contract) != before {
		t.Fatal("duplicate generation modified the contract")
	}
}

func TestInvalidSharedFileDoesNotLeavePartialResource(t *testing.T) {
	resourceFixture(t)
	original, _ := os.ReadFile("api/openapi.yaml")
	if err := os.WriteFile("cmd/api/resources.go", []byte("package main"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := addResource(newResource("Order"), false); err == nil {
		t.Fatal("invalid composition accepted")
	}
	if _, err := os.Stat("internal/modules/order"); !os.IsNotExist(err) {
		t.Fatalf("partial resource exists: %v", err)
	}
	after, _ := os.ReadFile("api/openapi.yaml")
	if string(after) != string(original) {
		t.Fatal("contract changed after preflight failed")
	}
}

func TestApplyChangesRollsBackOnWriteFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("blocked", 0o750); err != nil {
		t.Fatal(err)
	}
	err := applyChanges([]change{{path: "first.go", data: []byte("package p"), mode: 0o600}, {path: "blocked", data: []byte("x"), mode: 0o600}})
	if err == nil {
		t.Fatal("expected write failure")
	}
	if _, err := os.Stat("first.go"); !os.IsNotExist(err) {
		t.Fatalf("new file not rolled back: %v", err)
	}
}

func TestNextMigrationVersionRejectsDuplicates(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"00001_first.sql", "00001_second.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := nextMigrationVersion(dir); err == nil || !strings.Contains(err.Error(), "duplicate migration") {
		t.Fatalf("got %v", err)
	}
}

func TestResourceDryRunDoesNotWrite(t *testing.T) {
	resourceFixture(t)
	before, _ := os.ReadFile("api/openapi.yaml")
	if err := run([]string{"add", "resource", "Order", "-dry-run"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile("api/openapi.yaml")
	if string(after) != string(before) {
		t.Fatal("dry run modified contract")
	}
	if _, err := os.Stat("internal/modules/order"); !os.IsNotExist(err) {
		t.Fatalf("dry run created resource: %v", err)
	}
	if _, err := os.Stat(".forge-resource.lock"); !os.IsNotExist(err) {
		t.Fatalf("dry run left a lock: %v", err)
	}
}

func TestResourceAcceptsPostgresIdentifierBoundary(t *testing.T) {
	resourceFixture(t)
	res := newResource(strings.Repeat("A", 53))
	index := res.Table + "_list_idx"
	if len(index) != 63 {
		t.Fatalf("fixture index has %d bytes, want 63", len(index))
	}
	for _, dryRun := range []bool{true, false} {
		if err := addResource(res, dryRun); err != nil {
			t.Fatalf("63-byte index rejected (dry-run=%v): %v", dryRun, err)
		}
	}
	migration, err := os.ReadFile(filepath.Join("migrations", "00001_create_"+res.Table+".sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migration), `CREATE INDEX "`+index+`" ON "`+res.Table+`"`) {
		t.Fatalf("migration changed the boundary identifier: %s", migration)
	}
}

func TestResourceRejectsLongPostgresIdentifiersBeforeWriting(t *testing.T) {
	for _, tc := range []struct{ name, resource, identifier string }{
		{"long_index", strings.Repeat("A", 54), strings.Repeat("a", 54) + "s_list_idx"},
		{"long_table", strings.Repeat("A", 63), strings.Repeat("a", 63) + "s"},
		{"plural_expansion", strings.Repeat("b", 52) + "y", strings.Repeat("b", 52) + "ies_list_idx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, dryRun := range []bool{true, false} {
				resourceFixture(t)
				before := resourceFixtureFiles(t)
				err := addResource(newResource(tc.resource), dryRun)
				if err == nil || !strings.Contains(err.Error(), tc.identifier) || !strings.Contains(err.Error(), "63") || !strings.Contains(err.Error(), "shorten") {
					t.Fatalf("want actionable identifier error (dry-run=%v), got %v", dryRun, err)
				}
				if !maps.Equal(before, resourceFixtureFiles(t)) {
					t.Fatalf("identifier rejection changed project files (dry-run=%v)", dryRun)
				}
			}
		})
	}
}

func resourceFixtureFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[path+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(path)
		files[path] = string(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestResourceQuotesPostgresIdentifiers(t *testing.T) {
	for _, name := range []string{"Reference", "Value"} {
		t.Run(name, func(t *testing.T) {
			resourceFixture(t)
			res := newResource(name)
			if err := addResource(res, false); err != nil {
				t.Fatal(err)
			}
			migration, err := os.ReadFile(filepath.Join("migrations", "00001_create_"+res.Table+".sql"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{`CREATE TABLE "` + res.Table + `"`, `CREATE INDEX "` + res.Table + `_list_idx" ON "` + res.Table + `"`, `DROP TABLE "` + res.Table + `";`} {
				if !strings.Contains(string(migration), want) {
					t.Errorf("migration missing quoted identifier %s", want)
				}
			}
			queries, err := os.ReadFile(filepath.Join("internal", "modules", res.Snake, "queries.sql"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(queries), `"`+res.Table+`"`) != 6 {
				t.Fatalf("each CRUD/count query must quote its table: %s", queries)
			}
		})
	}
}

func TestResourceGeneratorHonorsCheckoutLock(t *testing.T) {
	resourceFixture(t)
	if err := os.WriteFile(".forge-resource.lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := addResource(newResource("Order"), false); err == nil {
		t.Fatal("concurrent generation accepted")
	}
	if _, err := os.Stat(".forge-resource.lock"); err != nil {
		t.Fatal("removed another generator's lock")
	}
}

func TestApplyChangesRestoresExistingFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	original := []byte("original")
	if err := os.WriteFile("shared", original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("blocked", 0o750); err != nil {
		t.Fatal(err)
	}
	err := applyChanges([]change{
		{path: "shared", data: []byte("changed"), previous: original, mode: 0o600, exists: true},
		{path: "blocked", data: []byte("x"), mode: 0o600},
	})
	if err == nil {
		t.Fatal("expected write failure")
	}
	got, err := os.ReadFile("shared")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("shared file was not restored: %s", got)
	}
}

func TestCompositionAliasesAvoidInfrastructureImports(t *testing.T) {
	resourceFixture(t)
	for _, name := range []string{"Api", "Http", "Idempotency"} {
		if err := addResource(newResource(name), false); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.ReadFile("cmd/api/resources.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"resourceAPI", "resourceHTTP", "resourceIdempotency"} {
		if !strings.Contains(string(source), alias+".NewHandler(") {
			t.Errorf("missing distinct alias %s", alias)
		}
	}
}

func TestResourceRejectsReservedNamesBeforeWriting(t *testing.T) {
	for _, name := range []string{"Type", "Unimplemented", "ServerInterface", "Handler", "Con", "Prn", "Aux", "Nul", "Com1", "Com9", "Lpt1", "Lpt9"} {
		t.Run(name, func(t *testing.T) {
			resourceFixture(t)
			if err := addResource(newResource(name), false); err == nil {
				t.Fatal("reserved name accepted")
			}
			if _, err := os.Stat("internal"); !os.IsNotExist(err) {
				t.Fatalf("source written before name validation: %v", err)
			}
		})
	}
}

func TestResourceRejectsExistingGeneratedDeclarations(t *testing.T) {
	resourceFixture(t)
	if err := os.MkdirAll("internal/gen/api", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("internal/gen/api/api.gen.go", []byte("package api\ntype OrderInput struct {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := addResource(newResource("Order"), false)
	if err == nil || !strings.Contains(err.Error(), "OrderInput") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat("internal/modules"); !os.IsNotExist(err) {
		t.Fatalf("resource written after declaration collision: %v", err)
	}
}

func TestResourceRejectsLinkedOutputParent(t *testing.T) {
	resourceFixture(t)
	outside := t.TempDir()
	if err := os.MkdirAll("internal", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, "internal/modules"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := addResource(newResource("Order"), false)
	if err == nil || !strings.Contains(err.Error(), "linked output") {
		t.Fatalf("got %v", err)
	}
	files, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("generator wrote outside the project")
	}
}

func TestResourceRejectsConflictingContractComponents(t *testing.T) {
	resourceFixture(t)
	source, err := os.ReadFile("api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	source = []byte(strings.Replace(string(source), "  schemas:\n", "  schemas:\n    OrderInput: {type: object}\n", 1))
	if err := os.WriteFile("api/openapi.yaml", source, 0o600); err != nil {
		t.Fatal(err)
	}
	err = addResource(newResource("Order"), false)
	if err == nil || !strings.Contains(err.Error(), "OrderInput") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat("internal"); !os.IsNotExist(err) {
		t.Fatalf("source written after contract collision: %v", err)
	}
}

func TestGoTemplatesParseForEveryOptionalCombination(t *testing.T) {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	for _, outbox := range []bool{false, true} {
		for _, idempotency := range []bool{false, true} {
			res := newResource("Order")
			res.Outbox, res.Idempotency = outbox, idempotency
			for _, entry := range entries {
				if !strings.HasSuffix(entry.Name(), ".go.tmpl") {
					continue
				}
				src, err := render(entry.Name(), res)
				if err != nil {
					t.Fatal(err)
				}
				if entry.Name() == "delegates.go.tmpl" {
					src = append([]byte("package main\n"), src...)
				}
				if _, err := format.Source(src); err != nil {
					t.Errorf("%s outbox=%v idempotency=%v: %v", entry.Name(), outbox, idempotency, err)
				}
			}
		}
	}
}
