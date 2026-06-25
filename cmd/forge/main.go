// Command forge is the day-2 generator. It stamps a new resource module in the
// same vertical-slice shape as internal/modules/widget, so a service keeps
// evolving with one command instead of hand-copying boilerplate.
//
// Usage:
//
//	forge add resource <Name>
//
// After generating, add the queries file to sqlc.yaml, run `task generate`, and
// mount the resource's handler in your router.
package main

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
	"unicode"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

const moduleName = "github.com/y0f/go-api-scaffolding"

type resource struct {
	Pascal string
	Camel  string
	Snake  string
	Table  string
	Module string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "forge:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 3 || args[0] != "add" || args[1] != "resource" {
		return fmt.Errorf("usage: forge add resource <Name>")
	}
	res := newResource(args[2])
	if res.Snake == "" {
		return fmt.Errorf("invalid resource name %q", args[2])
	}

	version := time.Now().UTC().Format("20060102150405")
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

	for _, t := range targets {
		if err := renderFile(t.tmpl, t.path, res); err != nil {
			return err
		}
		fmt.Println("created", t.path)
	}

	printNextSteps(res)
	return nil
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
	if err := os.MkdirAll(filepath.Dir(outPath), 0o750); err != nil {
		return err
	}
	return os.WriteFile(outPath, buf.Bytes(), 0o600)
}

func printNextSteps(res resource) {
	fmt.Printf(`
Next steps:
  1. Add the queries file to sqlc.yaml under sql[0].queries:
       - internal/modules/%s/queries.sql
  2. Regenerate type-safe code:
       task generate
  3. Mount the handler in your router setup:
       %s.NewHandler(%s.NewService(%s.NewRepository(pool), logger)).Mount(r)
  4. Apply the new migration:
       task migrate
`, res.Snake, res.Snake, res.Snake, res.Snake)
}

func newResource(name string) resource {
	words := splitWords(name)
	snake := strings.Join(words, "_")
	pascal := toPascal(words)
	return resource{
		Pascal: pascal,
		Camel:  toCamel(pascal),
		Snake:  snake,
		Table:  pluralize(snake),
		Module: moduleName,
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
	for i, r := range s {
		switch {
		case r == '_' || r == '-' || r == ' ':
			flush()
		case unicode.IsUpper(r):
			if i > 0 {
				flush()
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
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

func toCamel(pascal string) string {
	if pascal == "" {
		return ""
	}
	runes := []rune(pascal)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// pluralize appends "s". It is intentionally naive; rename the table in the
// generated migration if a different plural is needed.
func pluralize(snake string) string {
	if snake == "" {
		return ""
	}
	return snake + "s"
}
