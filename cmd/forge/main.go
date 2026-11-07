// Command forge is the project's generator. It stamps a new resource module as
// a vertical slice (SQL, store, service, handler, test, migration), so a service
// keeps evolving with one command instead of hand-copying boilerplate.
//
// New resources follow the same OpenAPI contract, validation and typed handler
// workflow as the example. Writes require explicit per-resource permissions.
// SQL, migrations, contract operations and composition are added together.
//
// Add a resource:
//
//	forge add resource <Name>
//
// After adding a resource, run `task generate` and apply its migration.
package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// moduleName is the scaffold's own module path. forge init rewrites it in the
// project's source; add resource reads the real path from go.mod instead, so a
// forge binary built elsewhere still writes the project's own import paths.
const moduleName = "github.com/y0f/gophling/v2"

var usage = "usage: forge add resource <Name> [-dry-run]"

type resource struct {
	Pascal string
	Plural string
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

	flags := flag.NewFlagSet("forge add resource", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", false, "validate and list changes without writing files")
	if err := flags.Parse(args[3:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New(usage)
	}
	return addResource(res, *dryRun)
}

const sqlcConfig = "sqlc.yaml"

// addGoOption adds entry under key in the gen.go block of sqlc.yaml, creating
// the key when it is missing. An entry already present is left alone.
func addGoOption(src, key, entry string) (string, error) {
	const goBlock = "      go:\n"
	const indent = "        "
	line := indent + "  " + entry + "\n"
	header := indent + key + ":\n"
	if i := strings.Index(src, header); i >= 0 {
		at := i + len(header)
		if strings.Contains(src[at:], line) {
			return src, nil
		}
		return src[:at] + line + src[at:], nil
	}
	i := strings.Index(src, goBlock)
	if i < 0 {
		return "", fmt.Errorf("%s: no gen.go block found", sqlcConfig)
	}
	at := i + len(goBlock)
	return src[:at] + header + line + src[at:], nil
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
	versions := map[int]string{}
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
		if previous, ok := versions[n]; ok {
			return "", fmt.Errorf("duplicate migration version %d: %s and %s; renumber the unapplied migration before generating", n, previous, name)
		}
		versions[n] = name
		if n > highest {
			highest = n
		}
	}
	return fmt.Sprintf("%05d", highest+1), nil
}

func printNextSteps(res resource) {
	fmt.Printf(`
Registered /v1/%s in api/openapi.yaml and cmd/api/resources.go.
Next steps:
  1. go run ./cmd/dev generate
  2. Grant %q to the roles that may write in internal/auth/principal.go,
     or issue a token carrying that scope. Writes remain denied otherwise.
  3. go run ./cmd/migrate up
  4. go run ./cmd/dev check
`, res.Table, res.Table+":write")
}

// projectModule returns the module path declared in ./go.mod, falling back to
// moduleName when there is no readable module directive.
func projectModule() string {
	src, err := os.ReadFile("go.mod")
	if err != nil {
		return moduleName
	}
	for line := range strings.Lines(string(src)) {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "module" {
			if mod := strings.Trim(f[1], "\"`"); mod != "" {
				return mod
			}
		}
	}
	return moduleName
}

func newResource(name string) resource {
	words := splitWords(name)
	snake := strings.Join(words, "_")
	pascal := toPascal(words)
	camel := ""
	if len(words) != 0 {
		camel = words[0] + toPascal(words[1:])
	}
	return resource{
		Pascal:      pascal,
		Plural:      toPascal(splitWords(pluralize(snake))),
		Camel:       camel,
		Snake:       snake,
		Table:       pluralize(snake),
		Module:      projectModule(),
		Outbox:      hasPackageSources(filepath.Join("internal", "outbox")),
		Idempotency: hasPackageSources(filepath.Join("internal", "idempotency")),
	}
}

// Init preserves unrelated local files, so a retained notes-only directory does
// not mean an optional package is still part of the project.
func hasPackageSources(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") && !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, ".") {
			return true
		}
	}
	return false
}

// splitWords breaks snake_case, kebab-case, camelCase, and PascalCase into
// lowercase words, preserving the boundary between acronyms and later words.
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
		upper := strings.ToUpper(w)
		switch {
		case isInitialism(upper):
			b.WriteString(upper)
		case strings.HasSuffix(w, "s") && isInitialism(strings.TrimSuffix(upper, "S")):
			b.WriteString(strings.TrimSuffix(upper, "S") + "s")
		default:
			runes := []rune(w)
			runes[0] = unicode.ToUpper(runes[0])
			b.WriteString(string(runes))
		}
	}
	return b.String()
}

func isInitialism(word string) bool {
	const words = " ACL API ASCII CPU CSS DNS EOF GUID HTML HTTP HTTPS ID IP JSON QPS RAM RPC SLA SMTP SQL SSH TCP TLS TTL UDP UI UID UUID URI URL UTF8 VM XML XMPP XSRF XSS "
	return strings.Contains(words, " "+word+" ")
}

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
