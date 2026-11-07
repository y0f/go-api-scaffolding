package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/getkin/kin-openapi/openapi3"
)

// change is prepared in memory before any project source is modified.
type change struct {
	path     string
	data     []byte
	previous []byte
	mode     os.FileMode
	exists   bool
}

func addResource(res resource, dryRun bool) error {
	if err := validateResourceName(res); err != nil {
		return err
	}
	// Serialise generators in one checkout. Separate worktrees must resolve
	// migration versions and regenerate shared output when integrated.
	lock, err := os.OpenFile(".forge-resource.lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("acquire .forge-resource.lock: %w; inspect the lock's PID and action, and remove it only after confirming that process has stopped; review git diff for interrupted writes before retrying", err)
	}
	defer func() { _ = os.Remove(".forge-resource.lock") }()
	if _, err := fmt.Fprintf(lock, "pid=%d\naction=add resource %s\n", os.Getpid(), res.Pascal); err != nil {
		_ = lock.Close()
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}

	version, err := nextMigrationVersion("migrations")
	if err != nil {
		return err
	}
	if err := validateResourceTable(res.Table); err != nil {
		return err
	}
	dir := filepath.Join("internal", "modules", res.Snake)
	targets := []struct{ tmpl, path string }{
		{"domain.go.tmpl", filepath.Join(dir, resourceDomainFile(res))},
		{"store.go.tmpl", filepath.Join(dir, "store.go")},
		{"service.go.tmpl", filepath.Join(dir, "service.go")},
		{"handler.go.tmpl", filepath.Join(dir, "handler.go")},
		{"service_test.go.tmpl", filepath.Join(dir, "service_test.go")},
		{"queries.sql.tmpl", filepath.Join(dir, "queries.sql")},
		{"migration.sql.tmpl", filepath.Join("migrations", version+"_create_"+res.Table+".sql")},
	}
	if res.Idempotency {
		targets = append(targets,
			struct{ tmpl, path string }{"idempotent.go.tmpl", filepath.Join(dir, "idempotent.go")},
			struct{ tmpl, path string }{"idempotent_test.go.tmpl", filepath.Join(dir, "idempotent_test.go")},
		)
	}
	changes := make([]change, 0, len(targets)+3)
	for _, target := range targets {
		if err := rejectLinkedParents(target.path); err != nil {
			return err
		}
		if _, err := os.Lstat(target.path); err == nil {
			return fmt.Errorf("%s already exists, refusing to overwrite", target.path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		data, err := render(target.tmpl, res)
		if err != nil {
			return err
		}
		if strings.HasSuffix(target.path, ".go") {
			if data, err = format.Source(data); err != nil {
				return fmt.Errorf("format %s: %w", target.path, err)
			}
		}
		changes = append(changes, change{path: target.path, data: data, mode: 0o600})
	}
	for _, shared := range []struct {
		path string
		edit func(string, resource) (string, error)
	}{
		{sqlcConfig, addSQLConfig},
		{"api/openapi.yaml", addContract},
		{"cmd/api/resources.go", addComposition},
	} {
		if err := rejectLinkedParents(shared.path); err != nil {
			return err
		}
		info, err := os.Lstat(shared.path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s must be a regular file", shared.path)
		}
		before, err := os.ReadFile(shared.path)
		if err != nil {
			return err
		}
		after, err := shared.edit(strings.ReplaceAll(string(before), "\r\n", "\n"), res)
		if err != nil {
			return err
		}
		changes = append(changes, change{path: shared.path, data: []byte(after), previous: before, mode: info.Mode().Perm(), exists: true})
	}
	if dryRun {
		for _, c := range changes {
			fmt.Println("would update", c.path)
		}
		return nil
	}
	if err := applyChanges(changes); err != nil {
		return err
	}
	for _, c := range changes {
		fmt.Println("updated", c.path)
	}
	printNextSteps(res)
	return nil
}

func resourceDomainFile(res resource) string {
	// Compound names can end in Go's _test, _GOOS or _GOARCH file suffixes.
	// Fixed template filenames must also remain distinct from the domain file.
	name := res.Snake
	if strings.Contains(name, "_") || name == "store" || name == "service" || name == "idempotent" {
		name += "_model"
	}
	return name + ".go"
}

func render(name string, res resource) ([]byte, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/"+name)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, res); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func addSQLConfig(src string, res resource) (string, error) {
	const key = "    queries:\n"
	if !strings.Contains(src, key) {
		return "", fmt.Errorf("%s: no queries list found", sqlcConfig)
	}
	src = strings.Replace(src, key, key+"      - internal/modules/"+res.Snake+"/queries.sql\n", 1)
	var err error
	if src, err = addGoOption(src, "inflection_exclude_table_names", "- "+res.Table); err != nil {
		return "", err
	}
	return addGoOption(src, "rename", res.Table+": "+res.Pascal)
}

func addContract(src string, res resource) (string, error) {
	if !strings.Contains(src, "paths:\n") {
		return "", errors.New("api/openapi.yaml: expected a paths mapping")
	}
	if strings.Contains(src, "  /v1/"+res.Table+":") {
		return "", fmt.Errorf("OpenAPI resource %s already exists", res.Pascal)
	}
	for _, name := range []string{res.Pascal, res.Pascal + "Input", res.Pascal + "List", res.Pascal + "ID"} {
		if strings.Contains(src, "\n    "+name+":") {
			return "", fmt.Errorf("OpenAPI component %s already exists", name)
		}
	}
	paths, err := render("paths.yaml.tmpl", res)
	if err != nil {
		return "", err
	}
	schemas, err := render("schemas.yaml.tmpl", res)
	if err != nil {
		return "", err
	}
	src = strings.Replace(src, "paths:\n", "paths:\n"+string(paths), 1)
	const parameters = "\n  parameters:\n"
	const schemaKey = "\n  schemas:\n"
	if !strings.Contains(src, parameters) || !strings.Contains(src, schemaKey) {
		return "", errors.New("api/openapi.yaml: expected components parameters and schemas")
	}
	param := fmt.Sprintf("    %sID:\n      name: id\n      in: path\n      required: true\n      schema: { type: string, format: uuid }\n", res.Pascal)
	src = strings.Replace(src, parameters, parameters+param, 1)
	src = strings.Replace(src, schemaKey, schemaKey+string(schemas), 1)
	doc, err := openapi3.NewLoader().LoadFromData([]byte(src))
	if err != nil {
		return "", fmt.Errorf("load updated OpenAPI contract: %w", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		return "", fmt.Errorf("validate updated OpenAPI contract: %w", err)
	}
	return src, nil
}

func addComposition(src string, res resource) (string, error) {
	alias := "resource" + res.Pascal
	wiring := fmt.Sprintf("app.%s = %s.NewHandler(%s.NewService(%s.NewRepository(pool)))\n", res.Camel, alias, alias, alias)
	if res.Idempotency {
		wiring += fmt.Sprintf("app.%s.EnableIdempotency(idemStore)\n", res.Camel)
	}
	for _, item := range []struct{ marker, value string }{
		{"// forge:resource-imports", fmt.Sprintf("%s \"%s/internal/modules/%s\"\n", alias, res.Module, res.Snake)},
		{"// forge:resource-fields", fmt.Sprintf("%s *%s.Handler\n", res.Camel, alias)},
		{"// forge:resource-wiring", wiring},
	} {
		if strings.Count(src, item.marker) != 1 {
			return "", fmt.Errorf("cmd/api/resources.go: expected one %s marker", item.marker)
		}
		src = strings.Replace(src, item.marker, item.value+item.marker, 1)
	}
	// A minimal initialized project has no handlers and goimports removes
	// net/http. Restore it when adding the first resource.
	if !strings.Contains(src, "\"net/http\"") {
		src = strings.Replace(src, "import (", "import (\n\"net/http\"\n", 1)
	}
	delegates, err := render("delegates.go.tmpl", res)
	if err != nil {
		return "", err
	}
	out, err := format.Source([]byte(src + "\n" + string(delegates)))
	if err != nil {
		return "", fmt.Errorf("format composition: %w", err)
	}
	return string(out), nil
}

// applyChanges rolls back completed writes if any later write fails. Existing
// files retain their permissions; new files are never silently overwritten.
func applyChanges(changes []change) error {
	var applied []change
	for _, c := range changes {
		if err := writeChange(c); err != nil {
			errs := []error{err}
			for i := len(applied) - 1; i >= 0; i-- {
				prev := applied[i]
				var rollbackErr error
				if prev.exists {
					rollbackErr = os.WriteFile(prev.path, prev.previous, prev.mode)
				} else {
					rollbackErr = os.Remove(prev.path)
				}
				if rollbackErr != nil {
					errs = append(errs, fmt.Errorf("rollback %s: %w", prev.path, rollbackErr))
				}
			}
			return errors.Join(errs...)
		}
		applied = append(applied, c)
	}
	return nil
}

func writeChange(c change) error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o750); err != nil {
		return err
	}
	if c.exists {
		temp, err := os.CreateTemp(filepath.Dir(c.path), ".forge-write-*")
		if err != nil {
			return err
		}
		name := temp.Name()
		defer func() { _ = os.Remove(name) }()
		if err := temp.Chmod(c.mode); err != nil {
			_ = temp.Close()
			return err
		}
		_, writeErr := temp.Write(c.data)
		closeErr := temp.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return err
		}
		return os.Rename(name, c.path)
	}
	f, err := os.OpenFile(c.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, c.mode)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(c.data)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(c.path)
		return err
	}
	return nil
}

// Resource names become package names and public API declarations. Reject
// collisions before touching sources, including declarations in customized codegen.
func validateResourceName(res resource) error {
	if !token.IsIdentifier(res.Snake) || token.Lookup(res.Snake).IsKeyword() {
		return fmt.Errorf("resource %q does not produce a valid Go package name", res.Pascal)
	}
	// PostgreSQL truncates identifiers beyond its default 63-byte limit. Keep
	// the table, implicit primary-key index and explicit list index distinct.
	for _, name := range []string{res.Table, res.Table + "_pkey", res.Table + "_list_idx"} {
		if len(name) > 63 {
			return fmt.Errorf("resource %s produces PostgreSQL identifier %q (%d bytes), exceeding the 63-byte limit; shorten the resource name so its pluralized table name is at most 54 bytes", res.Pascal, name, len(name))
		}
	}
	// Check on every host: a resource generated on Linux must still check out
	// and build on Windows, where these directory names are device aliases.
	switch res.Snake {
	case "con", "prn", "aux", "nul":
		return fmt.Errorf("resource %s uses a reserved Windows directory name", res.Pascal)
	}
	if len(res.Snake) == 4 && (strings.HasPrefix(res.Snake, "com") || strings.HasPrefix(res.Snake, "lpt")) && res.Snake[3] >= '1' && res.Snake[3] <= '9' {
		return fmt.Errorf("resource %s uses a reserved Windows directory name", res.Pascal)
	}
	reserved := map[string]bool{}
	for _, name := range []string{"Unimplemented", "ServerInterface", "ServerInterfaceWrapper", "MiddlewareFunc", "ChiServerOptions", "Handler", "HandlerFromMux", "HandlerFromMuxWithBaseURL", "HandlerWithOptions", "RegisterHandlers", "GetSpec"} {
		reserved[name] = true
	}
	generated := "internal/gen/api/api.gen.go"
	declarations, _, err := readGoDeclarations(generated)
	if err != nil {
		return err
	}
	for name := range declarations {
		reserved[name] = true
	}
	for _, name := range []string{res.Pascal, res.Pascal + "Input", res.Pascal + "List", res.Pascal + "ID", "List" + res.Plural + "Params", "Create" + res.Pascal + "JSONRequestBody", "Update" + res.Pascal + "JSONRequestBody"} {
		if reserved[name] {
			return fmt.Errorf("resource %s conflicts with API declaration %s; choose a different name", res.Pascal, name)
		}
	}
	return validateDatabaseNames(res)
}

func validateDatabaseNames(res resource) error {
	reserved := map[string]bool{"Querier": true, "Queries": true, "DBTX": true, "Dbtx": true, "New": true}
	methods := map[string]bool{}
	entries, err := os.ReadDir("internal/gen/db")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		names, queries, err := readGoDeclarations(filepath.Join("internal/gen/db", entry.Name()))
		if err != nil {
			return err
		}
		for name := range names {
			reserved[name] = true
		}
		for name := range queries {
			methods[name] = true
		}
	}
	if reserved[res.Pascal] {
		return fmt.Errorf("resource %s conflicts with a sqlc declaration; choose a different name", res.Pascal)
	}
	for _, query := range []string{"Create" + res.Pascal, "Get" + res.Pascal, "Update" + res.Pascal, "Delete" + res.Pascal, "List" + res.Plural, "Count" + res.Plural} {
		if methods[query] || reserved[query+"Params"] || reserved[query+"Row"] {
			return fmt.Errorf("resource %s conflicts with sqlc query %s or its Params/Row declarations", res.Pascal, query)
		}
	}
	return nil
}

// Read package declarations and methods on sqlc's Queries receiver separately:
// a method name can coexist with a package-level type of the same name.
func readGoDeclarations(path string) (map[string]bool, map[string]bool, error) {
	names, methods := map[string]bool{}, map[string]bool{}
	source, err := os.ReadFile(path) //#nosec G304 -- callers use fixed generated paths or entries read from internal/gen/db
	if errors.Is(err, os.ErrNotExist) {
		return names, methods, nil
	}
	if err != nil {
		return nil, nil, err
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("parse generated declarations in %s: %w", path, err)
	}
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					names[spec.Name.Name] = true
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						names[name.Name] = true
					}
				}
			}
		case *ast.FuncDecl:
			if declaration.Recv == nil {
				names[declaration.Name.Name] = true
				continue
			}
			if len(declaration.Recv.List) != 1 {
				return nil, nil, fmt.Errorf("invalid generated method receiver in %s", path)
			}
			receiver := declaration.Recv.List[0].Type
			if pointer, ok := receiver.(*ast.StarExpr); ok {
				receiver = pointer.X
			}
			if name, ok := receiver.(*ast.Ident); ok && name.Name == "Queries" {
				methods[declaration.Name.Name] = true
			}
		}
	}
	return names, methods, nil
}

// Refuse linked output directories, even links that currently point inside
// the checkout. This keeps generator writes in ordinary project directories.
func rejectLinkedParents(path string) error {
	if !filepath.IsLocal(path) {
		return fmt.Errorf("output path %q is outside the project", path)
	}
	for parent := filepath.Dir(path); parent != "."; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing linked output directory %s", parent)
		}
		if !info.IsDir() {
			return fmt.Errorf("output parent %s is not a directory", parent)
		}
	}
	return nil
}
