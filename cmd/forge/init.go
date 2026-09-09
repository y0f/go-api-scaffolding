package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"golang.org/x/term"
)

// A slice is an optional part of the scaffold. Its code is either a whole
// path, deleted when the slice is dropped, or a block of lines fenced by
// "forge:begin <name>" and "forge:end <name>" markers inside a shared file.
type slice struct {
	Name  string
	Title string
	Paths []string
}

var slices = []slice{
	{
		Name:  "example",
		Title: "Example resource: the spec-first widget module with its OpenAPI paths, migration and tests",
		Paths: []string{
			"internal/modules/widget",
			"internal/server/api_integration_test.go",
			"internal/server/idempotency_integration_test.go",
		},
	},
	{
		Name:  "outbox",
		Title: "Transactional outbox: events written with the state change, relayed by a poller",
		Paths: []string{"internal/outbox"},
	},
	{
		Name:  "idempotency",
		Title: "Idempotency keys: safe retries of unsafe requests via the Idempotency-Key header",
		Paths: []string{
			"internal/idempotency",
			"internal/modules/widget/idempotent.go",
			"internal/modules/widget/idempotent_test.go",
			"internal/modules/widget/idempotent_integration_test.go",
			"internal/server/idempotency_integration_test.go",
		},
	},
	{
		Name:  "otlp",
		Title: "OTLP trace export and the Collector, Tempo, Prometheus and Grafana compose profile",
		Paths: []string{"deployments/observability"},
	},
	{
		Name:  "admin",
		Title: "Admin listener: token-gated pprof and expvar on a separate port",
		Paths: []string{"internal/server/admin.go"},
	},
}

// A derived slice exists only to serve other slices and goes when all of them
// go: the reaper has nothing to reap without outbox or idempotency tables, and
// sqlc has nothing to generate without a queries file.
var derived = []struct {
	Name  string
	Of    []string
	Paths []string
}{
	{Name: "workers", Of: []string{"outbox", "idempotency"}, Paths: []string{"internal/maintenance"}},
	{Name: "sqlc", Of: []string{"example", "outbox", "idempotency"}, Paths: []string{"internal/gen/db"}},
}

// The installer removes itself: these paths and every "init" block go on
// every run, so the project keeps no trace of the scaffold's machinery.
var initPaths = []string{"cmd/forge/init.go", "cmd/forge/init_test.go", "docs/assets/logo.webp"}

func init() {
	usage = "usage: forge new <dir> | forge init | forge add resource <Name>"
}

// runNew clones the scaffold into dir and runs init there, so a project
// starts from one command: go run <scaffold module>/cmd/forge@latest new dir.
// Flags after dir are init's.
func runNew(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: forge new <dir> [-source git-url-or-path] [init flags]")
	}
	dir, rest := args[0], args[1:]
	source := "https://" + moduleName
	if len(rest) >= 2 && rest[0] == "-source" {
		source, rest = rest[1], rest[2:]
	}
	if _, err := os.Stat(dir); err == nil { //#nosec G703 -- dir is the directory the user asked for
		return fmt.Errorf("%s already exists", dir)
	}
	for _, step := range [][]string{
		{"git", "clone", "--quiet", source, dir},
	} {
		if err := execStep(step...); err != nil {
			return err
		}
	}
	// The clone's history is the scaffold's, not the project's.
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil { //#nosec G703 -- inside the clone just made
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	// init works from git's file list, so the fresh repository stages
	// everything first; the first commit is the user's.
	for _, step := range [][]string{
		{"git", "init", "--quiet"},
		{"git", "add", "--all"},
	} {
		if err := execStep(step...); err != nil {
			return err
		}
	}
	viaNew = true
	if err := runInit(rest); err != nil {
		return err
	}
	fmt.Printf("\nNext: cd %s && git commit -m 'Initial commit' && task up\n", dir)
	return nil
}

func execStep(argv ...string) error {
	cmd := exec.Command(argv[0], argv[1:]...) //#nosec G204 G702 -- fixed argv apart from the clone source and directory the user typed
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return nil
}

var markerRe = regexp.MustCompile(`\bforge:(begin|end) ([a-z]+)\b`)

func runInit(args []string) error {
	fs := flag.NewFlagSet("forge init", flag.ContinueOnError)
	module := fs.String("module", "", "module path for the project, for example github.com/you/app")
	drop := fs.String("drop", "", "comma-separated slices to remove: "+strings.Join(sliceNames(), ","))
	yes := fs.Bool("yes", false, "do not prompt (requires -module)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(initPaths[0]); err != nil {
		return errors.New("run from the repository root of an uninitialised scaffold")
	}

	dropped := map[string]bool{}
	for _, name := range strings.Split(*drop, ",") {
		if name = strings.TrimSpace(name); name != "" {
			if !isSlice(name) {
				return fmt.Errorf("unknown slice %q (want one of %s)", name, strings.Join(sliceNames(), ", "))
			}
			dropped[name] = true
		}
	}
	if *yes {
		if *module == "" {
			return errors.New("-yes requires -module")
		}
	} else {
		if !term.IsTerminal(int(os.Stdin.Fd())) && os.Getenv("ACCESSIBLE") == "" {
			return errors.New("no terminal: pass -yes with -module and -drop, or set ACCESSIBLE=1 for line-based prompts")
		}
		if err := prompt(module, dropped); err != nil {
			return err
		}
	}
	if err := validateModule(*module); err != nil {
		return err
	}
	if err := apply(*module, dropped); err != nil {
		return err
	}
	if !viaNew {
		fmt.Println("\nReview with git diff, commit, then `task up`.")
	}
	return nil
}

// viaNew is set when init runs as the last step of `forge new`, which prints
// its own next step.
var viaNew bool

func prompt(module *string, dropped map[string]bool) error {
	options := make([]huh.Option[string], 0, len(slices))
	keep := make([]string, 0, len(slices))
	for _, s := range slices {
		options = append(options, huh.NewOption(s.Title, s.Name).Selected(!dropped[s.Name]))
		if !dropped[s.Name] {
			keep = append(keep, s.Name)
		}
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Module path").
			Description("Becomes the module in go.mod and every import path.").
			Placeholder("github.com/you/app").
			Value(module).
			Validate(validateModule),
		huh.NewMultiSelect[string]().
			Title("Keep").
			Description("Everything else is deleted, along with this installer. Space toggles, enter confirms.").
			Options(options...).
			Filterable(false).
			Value(&keep),
	))
	// Space is what people reach for on a checklist; the default binding is x.
	keys := huh.NewDefaultKeyMap()
	keys.MultiSelect.Toggle = key.NewBinding(key.WithKeys(" ", "x"), key.WithHelp("space", "toggle"))
	form = form.WithKeyMap(keys)
	// ACCESSIBLE switches to plain line-based prompts, for screen readers and
	// for terminals that cannot render the interactive form. Each of its fields
	// buffers its own reads, so input is handed over one line at a time or the
	// first prompt would swallow the answers meant for the ones after it. The
	// interactive form must keep reading the console itself, so the reader is
	// only substituted in that mode.
	if os.Getenv("ACCESSIBLE") != "" {
		form = form.WithAccessible(true).WithInput(lineReader{os.Stdin})
	}
	if err := form.Run(); err != nil {
		return err
	}
	kept := map[string]bool{}
	for _, name := range keep {
		kept[name] = true
	}
	for _, s := range slices {
		dropped[s.Name] = !kept[s.Name]
	}
	return nil
}

// lineReader returns at most one line per Read.
type lineReader struct{ r io.Reader }

func (l lineReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		var b [1]byte
		if _, err := l.r.Read(b[:]); err != nil {
			return n, err
		}
		p[n] = b[0]
		n++
		if b[0] == '\n' {
			break
		}
	}
	return n, nil
}

var modulePathRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]*\.[a-z]+(/[A-Za-z0-9._\-]+)+$`)

func validateModule(module string) error {
	if !modulePathRe.MatchString(module) {
		return errors.New("module path must look like host.tld/owner/name")
	}
	return nil
}

// apply performs the whole transformation in dependency order: text first, so
// a failure leaves files intact rather than half-deleted; then deletions; then
// the toolchain steps that prove the result builds.
func apply(module string, dropped map[string]bool) error {
	for _, d := range derived {
		all := true
		for _, of := range d.Of {
			all = all && dropped[of]
		}
		dropped[d.Name] = all
	}

	removed := append([]string{}, initPaths...)
	for _, s := range slices {
		if dropped[s.Name] {
			removed = append(removed, s.Paths...)
		}
	}
	for _, d := range derived {
		if dropped[d.Name] {
			removed = append(removed, d.Paths...)
		}
	}

	files, err := trackedFiles()
	if err != nil {
		return err
	}
	for _, file := range files {
		if underAny(file, removed) {
			continue
		}
		src, err := os.ReadFile(file) //#nosec G304 -- path comes from git ls-files
		if err != nil {
			return err
		}
		if bytes.IndexByte(src, 0) >= 0 {
			continue // binary
		}
		out, err := strip(string(src), dropped)
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		out = strings.ReplaceAll(out, moduleName, module)
		out = strings.ReplaceAll(out, path.Base(moduleName), path.Base(module))
		if out != string(src) {
			if err := os.WriteFile(file, []byte(out), 0o600); err != nil {
				return err
			}
		}
	}
	for _, path := range removed {
		if err := os.RemoveAll(filepath.FromSlash(path)); err != nil {
			return err
		}
	}

	// goimports runs before tidy: a dropped block can leave an import of a
	// package that no longer exists, and tidy would go looking for it.
	steps := [][]string{
		{
			"go", "-C", "tools", "build", "-o", "../bin/",
			"github.com/sqlc-dev/sqlc/cmd/sqlc",
			"github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen",
			"golang.org/x/tools/cmd/goimports",
		},
		{bin("goimports"), "-w", "cmd", "internal"},
		{"go", "mod", "tidy"},
		{"go", "-C", "tools", "mod", "tidy"},
	}
	if !dropped["sqlc"] {
		steps = append(steps, []string{bin("sqlc"), "generate"})
	}
	steps = append(steps,
		[]string{bin("oapi-codegen"), "--config", "api/oapi-codegen.yaml", "api/openapi.yaml"},
		[]string{"go", "build", "./..."},
		[]string{"go", "vet", "./..."},
	)
	for _, step := range steps {
		fmt.Println("$", strings.Join(step, " "))
		if err := execStep(step...); err != nil {
			return err
		}
	}

	kept := []string{}
	gone := []string{}
	for _, s := range slices {
		if dropped[s.Name] {
			gone = append(gone, s.Name)
		} else {
			kept = append(kept, s.Name)
		}
	}
	fmt.Printf("\n%s is ready.\n  kept:    %s\n  removed: %s\n", module, orNone(kept), orNone(gone))
	return nil
}

// strip removes every block fenced for a dropped slice and the marker lines of
// every block that stays. Blocks may nest. It fails on an unbalanced or unknown
// marker, so a typo in the scaffold is caught by the test that runs it over
// every tracked file.
func strip(src string, dropped map[string]bool) (string, error) {
	var out strings.Builder
	var stack []string
	skipping := 0
	for _, line := range strings.SplitAfter(src, "\n") {
		if m := markerRe.FindStringSubmatch(line); m != nil {
			kind, name := m[1], m[2]
			if !isSlice(name) && !isDerived(name) && name != "init" {
				return "", fmt.Errorf("unknown marker %q", name)
			}
			switch kind {
			case "begin":
				stack = append(stack, name)
				if dropped[name] || name == "init" {
					skipping++
				}
			case "end":
				if len(stack) == 0 || stack[len(stack)-1] != name {
					return "", fmt.Errorf("forge:end %s without matching begin", name)
				}
				stack = stack[:len(stack)-1]
				if dropped[name] || name == "init" {
					skipping--
				}
			}
			continue
		}
		if skipping == 0 {
			out.WriteString(line)
		}
	}
	if len(stack) != 0 {
		return "", fmt.Errorf("forge:begin %s without matching end", stack[len(stack)-1])
	}
	return strings.TrimLeft(collapseBlankLines(out.String()), "\n"), nil
}

var blankRunRe = regexp.MustCompile(`\n{3,}`)

func collapseBlankLines(s string) string {
	return blankRunRe.ReplaceAllString(s, "\n\n")
}

func trackedFiles() ([]string, error) {
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files, nil
}

func underAny(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

func sliceNames() []string {
	names := make([]string, 0, len(slices))
	for _, s := range slices {
		names = append(names, s.Name)
	}
	return names
}

func isSlice(name string) bool {
	for _, s := range slices {
		if s.Name == name {
			return true
		}
	}
	return false
}

func isDerived(name string) bool {
	for _, d := range derived {
		if d.Name == name {
			return true
		}
	}
	return false
}

func bin(tool string) string {
	path := filepath.Join("bin", tool)
	if _, err := os.Stat(path + ".exe"); err == nil {
		return path + ".exe"
	}
	return path
}

func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
