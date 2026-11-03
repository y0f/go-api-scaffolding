package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sqlc singularizes table names with its own rules (quizes -> Quize), so the
// model name must be pinned to the Pascal name the templates reference.
func TestRegisterModel(t *testing.T) {
	t.Chdir(t.TempDir())
	src := "version: \"2\"\nsql:\n  - engine: postgresql\n    queries:\n      - internal/outbox/queries.sql\n    gen:\n      go:\n        out: internal/gen/db\n"
	if err := os.WriteFile(sqlcConfig, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, r := range []resource{newResource("Quiz"), newResource("News"), newResource("Quiz")} {
		if err := registerModel(r.Table, r.Pascal); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(sqlcConfig)
	want := "version: \"2\"\nsql:\n  - engine: postgresql\n    queries:\n      - internal/outbox/queries.sql\n    gen:\n      go:\n" +
		"        rename:\n          newses: News\n          quizes: Quiz\n" +
		"        inflection_exclude_table_names:\n          - newses\n          - quizes\n" +
		"        out: internal/gen/db\n"
	if string(got) != want {
		t.Fatalf("sqlc.yaml =\n%s\nwant\n%s", got, want)
	}

	if err := os.WriteFile(sqlcConfig, []byte("version: \"2\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := registerModel("orders", "Order"); err == nil {
		t.Error("expected an error when sqlc.yaml has no gen.go block")
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

// The hint must not tell the user to add a second Mounts field for the second
// resource, which is a duplicate-field compile error.
func TestNextStepsAppendToMounts(t *testing.T) {
	for _, idem := range []bool{false, true} {
		res := newResource("Invoice")
		res.Idempotency = idem
		out := captureStdout(t, func() { printNextSteps(res) })
		if !strings.Contains(out, "append invoiceHandler.Mount to the Mounts slice") {
			t.Errorf("idempotency=%v: hint does not say to append to Mounts:\n%s", idem, out)
		}
		if got := strings.Contains(out, "invoiceHandler.EnableIdempotency(idemStore)"); got != idem {
			t.Errorf("idempotency=%v: EnableIdempotency in hint = %v:\n%s", idem, got, out)
		}
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
