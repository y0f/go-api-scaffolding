// Command forge is the project's generator. It stamps a new resource module as
// a vertical slice (SQL, store, service, handler, test, migration), so a service
// keeps evolving with one command instead of hand-copying boilerplate.
//
// The generated module is secure by default but self-contained rather than
// spec-first: writes are authenticated with auth.Middleware and authorized
// against a per-resource permission, reads are public, and errors render as
// problem+json. Routing is plain chi instead of the generated OpenAPI server
// interface; make it spec-first by adding the paths to api/openapi.yaml and
// implementing that interface.
//
// Usage:
//
//	forge new <dir>             # clone the scaffold into dir and set it up there
//	forge init                  # set up this clone: pick a module path and the slices to keep
//	forge add resource <Name>
//
// After generating, run `task generate` and mount the resource's handler.
package main

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"unicode"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

const moduleName = "github.com/y0f/go-api-scaffolding"

var usage = "usage: forge add resource <Name>"

type resource struct {
	Pascal string
	Camel  string
	Snake  string
	Table  string
	Module string
	// Outbox and Idempotency are whether those packages exist in this project,
	// so the templates only use what is there.
	Outbox      bool
	Idempotency bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "forge:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	//forge:begin init
	if len(args) > 0 {
		switch args[0] {
		case "init":
			return runInit(args[1:])
		case "new":
			return runNew(args[1:])
		}
	}
	//forge:end init
	if len(args) < 3 || args[0] != "add" || args[1] != "resource" {
		return errors.New(usage)
	}
	res := newResource(args[2])
	if res.Snake == "" {
		return fmt.Errorf("invalid resource name %q", args[2])
	}

	version, err := nextMigrationVersion("migrations")
	if err != nil {
		return err
	}
	moduleDir := filepath.Join("internal", "modules", res.Snake)

	targets := []struct {
		tmpl string
		path string
	}{
		{"domain.go.tmpl", filepath.Join(moduleDir, res.Snake+".go")},
		{"store.go.tmpl", filepath.Join(moduleDir, "store.go")},
		{"service.go.tmpl", filepath.Join(moduleDir, "service.go")},
		{"handler.go.tmpl", filepath.Join(moduleDir, "handler.go")},
		{"service_test.go.tmpl", filepath.Join(moduleDir, "service_test.go")},
		{"queries.sql.tmpl", filepath.Join(moduleDir, "queries.sql")},
		{"migration.sql.tmpl", filepath.Join("migrations", version+"_create_"+res.Table+".sql")},
	}
	if res.Idempotency {
		targets = append(targets,
			struct{ tmpl, path string }{"idempotent.go.tmpl", filepath.Join(moduleDir, "idempotent.go")},
			struct{ tmpl, path string }{"idempotent_test.go.tmpl", filepath.Join(moduleDir, "idempotent_test.go")},
		)
	}

	for _, t := range targets {
		if err := renderFile(t.tmpl, t.path, res); err != nil {
			return err
		}
		fmt.Println("created", t.path)
	}
	if err := registerQueries(filepath.ToSlash(filepath.Join(moduleDir, "queries.sql"))); err != nil {
		return err
	}
	fmt.Println("registered the queries file in sqlc.yaml")

	printNextSteps(res)
	return nil
}

const sqlcConfig = "sqlc.yaml"

// registerQueries appends path to the queries list in sqlc.yaml, so a new module
// is picked up by `task generate` without a manual edit.
func registerQueries(path string) error {
	src, err := os.ReadFile(sqlcConfig)
	if err != nil {
		return err
	}
	if strings.Contains(string(src), "- "+path) {
		return nil
	}
	const key = "    queries:\n"
	i := strings.Index(string(src), key)
	if i < 0 {
		return fmt.Errorf("%s: no queries list found", sqlcConfig)
	}
	at := i + len(key)
	out := string(src[:at]) + "      - " + path + "\n" + string(src[at:])
	return os.WriteFile(sqlcConfig, []byte(out), 0o600) //#nosec G703 -- constant path; the inserted line comes from the validated resource name
}

// nextMigrationVersion returns the next sequential prefix for dir, matching the
// %05d scheme goose applies in order. Timestamped versions would sort after any
// later hand-written file and goose would then refuse to run it.
func nextMigrationVersion(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dir, err)
	}
	highest := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(prefix)
		if err != nil {
			continue
		}
		if n > highest {
			highest = n
		}
	}
	return fmt.Sprintf("%05d", highest+1), nil
}

func renderFile(tmplName, outPath string, res resource) error {
	if _, err := os.Stat(outPath); err == nil {
		return fmt.Errorf("%s already exists, refusing to overwrite", outPath)
	}
	tmpl, err := template.ParseFS(templateFS, "templates/"+tmplName)
	if err != nil {
		return fmt.Errorf("parse template %s: %w", tmplName, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, res); err != nil {
		return fmt.Errorf("render %s: %w", tmplName, err)
	}
	out := buf.Bytes()
	if strings.HasSuffix(outPath, ".go") {
		// Canonical formatting regardless of template whitespace, so a forged
		// module passes gofumpt untouched.
		if out, err = format.Source(out); err != nil {
			return fmt.Errorf("format %s: %w", outPath, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o750); err != nil {
		return err
	}
	return os.WriteFile(outPath, out, 0o600)
}

func printNextSteps(res resource) {
	mount := fmt.Sprintf("Mounts: []func(chi.Router){%s.NewHandler(%s.NewService(%s.NewRepository(pool)), verifier).Mount},",
		res.Snake, res.Snake, res.Snake)
	if res.Idempotency {
		mount = fmt.Sprintf(`%sHandler := %s.NewHandler(%s.NewService(%s.NewRepository(pool)), verifier)
       %sHandler.EnableIdempotency(idemStore)
       Mounts: []func(chi.Router){%sHandler.Mount},`, res.Camel, res.Snake, res.Snake, res.Snake, res.Camel, res.Camel)
	}
	fmt.Printf(`
Next steps:
  1. Regenerate type-safe code:
       task generate
  2. Mount the handler in cmd/api/main.go, in server.RouterDeps:
       %s
     with imports "github.com/go-chi/chi/v5" and "%s/internal/modules/%s".
  3. Grant write access in internal/auth/principal.go (rolePermissions) by adding
     %q to the roles that may write, or issue tokens carrying it as a scope.
  4. Apply the new migration (task up does this too):
       task migrate
`, mount, res.Module, res.Snake, res.Table+":write")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func newResource(name string) resource {
	words := splitWords(name)
	snake := strings.Join(words, "_")
	pascal := toPascal(words)
	camel := ""
	if pascal != "" {
		camel = strings.ToLower(pascal[:1]) + pascal[1:]
	}
	return resource{
		Pascal:      pascal,
		Camel:       camel,
		Snake:       snake,
		Table:       pluralize(snake),
		Module:      moduleName,
		Outbox:      exists(filepath.Join("internal", "outbox")),
		Idempotency: exists(filepath.Join("internal", "idempotency")),
	}
}

// splitWords breaks snake_case, kebab-case, camelCase, and PascalCase into
// lowercase words. Runs of capitals (such as ID) are not specially handled.
func splitWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case unicode.IsUpper(r):
			// A new word starts at an upper-case rune, except inside an
			// acronym: HTTPRoute splits as http, route.
			prevUpper := i > 0 && unicode.IsUpper(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if !prevUpper || nextLower {
				flush()
			}
			cur = append(cur, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur = append(cur, r)
		default:
			// Separators and anything that cannot be part of an identifier.
			flush()
		}
	}
	flush()
	if len(words) == 0 || !unicode.IsLetter([]rune(words[0])[0]) {
		return nil
	}
	return words
}

func toPascal(words []string) string {
	var b strings.Builder
	for _, w := range words {
		if w == "" {
			continue
		}
		runes := []rune(w)
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	return b.String()
}

// pluralize appends "s". It is intentionally naive; rename the table in the
// generated migration if a different plural is needed.
// pluralize covers the regular English forms; rename the table by hand for an
// irregular noun.
func pluralize(snake string) string {
	switch {
	case snake == "":
		return ""
	case strings.HasSuffix(snake, "s"), strings.HasSuffix(snake, "x"), strings.HasSuffix(snake, "z"),
		strings.HasSuffix(snake, "ch"), strings.HasSuffix(snake, "sh"):
		return snake + "es"
	case strings.HasSuffix(snake, "y") && len(snake) > 1 && !strings.ContainsRune("aeiou", rune(snake[len(snake)-2])):
		return snake[:len(snake)-1] + "ies"
	default:
		return snake + "s"
	}
}
