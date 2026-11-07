// Command dev provides the same portable development checks to people and agents.
// It uses only the standard library, so bootstrap needs Go and Git, not Task.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	goversion "go/version"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

var toolNames = []string{"air", "task", "golangci-lint", "oapi-codegen", "sqlc", "goimports", "govulncheck"}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dev:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: go run ./cmd/dev bootstrap|doctor|generate|generate-check|check|ci")
	}
	if _, err := os.Stat("tools/go.mod"); err != nil {
		return errors.New("run from the project root (tools/go.mod not found)")
	}
	if args[0] == "doctor" {
		return doctor(args[1:])
	}
	if len(args) != 1 {
		return fmt.Errorf("%s takes no arguments", args[0])
	}
	switch args[0] {
	case "bootstrap":
		return bootstrap()
	case "generate":
		if err := bootstrap(); err != nil {
			return err
		}
		return generate()
	case "generate-check":
		if err := bootstrap(); err != nil {
			return err
		}
		return checkGenerated("internal/gen", generate)
	case "check", "ci":
		if err := bootstrap(); err != nil {
			return err
		}
		if err := checkGenerated("internal/gen", generate); err != nil {
			return err
		}
		for _, command := range [][]string{
			{toolPath("golangci-lint"), "run", "./..."},
			{"go", "test", "-shuffle=on", "./..."},
			{"go", "build", "./..."},
		} {
			if err := execute(nil, command...); err != nil {
				return err
			}
		}
		if args[0] == "ci" {
			return fullChecks()
		}
		fmt.Println("Fast checks passed. Integration, race, vulnerabilities and containers were not checked; run go run ./cmd/dev ci for those gates.")
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func bootstrap() error {
	stamp, err := toolsStamp()
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile("bin/.tools-stamp"); err == nil && bytes.Equal(existing, stamp) && toolsExist() {
		return nil
	}
	if err := execute(nil, "go", "mod", "download"); err != nil {
		return err
	}
	toolchain, err := pinnedToolchain()
	if err != nil {
		return err
	}
	if err := execute([]string{"GOTOOLCHAIN=" + toolchain}, "go", "-C", "tools", "build", "-o", "../bin/", "tool"); err != nil {
		return err
	}
	if err := os.WriteFile("bin/.tools-stamp", stamp, 0o600); err != nil {
		return err
	}
	fmt.Println("Pinned tools are ready in bin/. Run: go run ./cmd/dev check")
	return nil
}

func pinnedToolchain() (string, error) {
	content, err := os.ReadFile("tools/go.mod")
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(content)) {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "toolchain" && strings.HasPrefix(fields[1], "go") {
			return fields[1], nil
		}
	}
	return "", errors.New("tools/go.mod must pin a toolchain for reproducible generation")
}

func toolsStamp() ([]byte, error) {
	h := sha256.New()
	for _, name := range []string{"go.mod", "go.sum", "tools/go.mod", "tools/go.sum"} {
		data, err := os.ReadFile(name) //#nosec G304 -- fixed repository paths
		if err != nil {
			return nil, err
		}
		_, _ = h.Write(data)
	}
	_, _ = fmt.Fprintf(h, "%s/%s", runtime.GOOS, runtime.GOARCH)
	return fmt.Appendf(nil, "%x\n", h.Sum(nil)), nil
}

func toolsExist() bool {
	for _, name := range toolNames {
		info, err := os.Stat(toolPath(name))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func toolPath(name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(".", "bin", name)
}

func generate() error {
	if err := checkMigrationVersions("migrations"); err != nil {
		return err
	}
	queries, err := filepath.Glob("internal/*/queries.sql")
	if err != nil {
		return err
	}
	modules, err := filepath.Glob("internal/modules/*/queries.sql")
	if err != nil {
		return err
	}
	if len(queries)+len(modules) > 0 {
		if err := execute(nil, toolPath("sqlc"), "generate"); err != nil {
			return err
		}
	}
	return execute(nil, toolPath("oapi-codegen"), "--config", "api/oapi-codegen.yaml", "api/openapi.yaml")
}

// Separate worktrees may allocate the same next migration number. Detect that
// merge conflict during the fast check, before sqlc or database access.
func checkMigrationVersions(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	versions := map[uint64]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			continue
		}
		version, err := strconv.ParseUint(prefix, 10, 64)
		if err != nil {
			continue
		}
		if previous, exists := versions[version]; exists {
			return fmt.Errorf("duplicate migration version %d: %s and %s; renumber the unapplied branch migration, then regenerate", version, previous, entry.Name())
		}
		versions[version] = entry.Name()
	}
	return nil
}

// checkGenerated compares content, rather than git's unstaged diff. This accepts
// correctly generated changes before a commit and detects drift in staged,
// unstaged, untracked, deleted, and newly created generated files alike.
func checkGenerated(dir string, regenerate func() error) error {
	before, err := snapshot(dir)
	if err != nil {
		return err
	}
	if err := regenerate(); err != nil {
		return err
	}
	after, err := snapshot(dir)
	if err != nil {
		return err
	}
	var changed []string
	for name, content := range before {
		current, exists := after[name]
		if !exists || !bytes.Equal(content, current) {
			changed = append(changed, name)
		}
	}
	for name := range after {
		if _, exists := before[name]; !exists {
			changed = append(changed, name)
		}
	}
	if len(changed) > 0 {
		slices.Sort(changed)
		return fmt.Errorf("generated code was stale (regenerated below); review and include these changes:\n%s", strings.Join(changed, "\n"))
	}
	return nil
}

func snapshot(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return files, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated files must not be symlinks: %s", path)
		}
		content, err := root.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.Join(dir, path)] = content
		return nil
	})
	return files, err
}

func fullChecks() error {
	if err := doctor([]string{"-integration", "-race", "-containers"}); err != nil {
		return err
	}
	for _, command := range [][]string{
		{toolPath("govulncheck"), "./..."},
		{"go", "test", "-race", "-shuffle=on", "./..."},
		{"go", "test", "-tags", "integration", "-race", "-count=1", "./..."},
		//forge:begin example
		{"go", "test", "-tags", "e2e", "-count=1", "-timeout", "15m", "./cmd/api"},
		//forge:end example
		//forge:begin init
		{"go", "test", "-tags", "generator", "-count=1", "./cmd/forge"},
		//forge:end init
		{"docker", "compose", "-f", "deployments/docker-compose.yml", "--profile", "*", "config", "--quiet"},
		{"docker", "compose", "-f", "deployments/docker-compose.yml", "--profile", "*", "pull", "--quiet"},
		{"docker", "build", "-f", "deployments/Dockerfile", "-t", "gophling:check", "."},
	} {
		if err := execute([]string{"CGO_ENABLED=1"}, command...); err != nil {
			return err
		}
	}
	fmt.Println("Full local checks passed. CI additionally checks the platform/Go-version/preset matrices and release configuration.")
	return nil
}

func doctor(args []string) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	integration := flags.Bool("integration", false, "require Docker or FORGE_TEST_DATABASE_URL")
	race := flags.Bool("race", false, "require a C compiler for race tests")
	containers := flags.Bool("containers", false, "require a running Docker daemon and Compose")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("doctor accepts flags only")
	}
	fmt.Printf("Go: %s (%s/%s)\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	minimum, err := checkGoVersion(runtime.Version())
	if err != nil {
		return err
	}
	fmt.Printf("Project minimum: %s; GOTOOLCHAIN=%s\n", minimum, os.Getenv("GOTOOLCHAIN"))
	_, gitErr := exec.LookPath("git")
	fmt.Printf("Git: %s\n", capability(gitErr == nil))
	fmt.Printf("Pinned tools: %s (bootstrap installs missing tools)\n", capability(toolsExist()))
	dockerOK := probe("docker", "info", "--format", "{{.ServerVersion}}") == nil
	composeOK := probe("docker", "compose", "version") == nil
	fmt.Printf("Docker daemon: %s; Compose: %s\n", capability(dockerOK), capability(composeOK))
	external := os.Getenv("FORGE_TEST_DATABASE_URL") != ""
	fmt.Printf("External test Postgres: %s (connectivity and CREATEDB permission are checked by integration tests)\n", capability(external))
	cc, err := exec.Command("go", "env", "CC").Output()
	compilerOK := false
	if err == nil {
		parts := strings.Fields(strings.TrimSpace(string(cc)))
		if len(parts) > 0 {
			_, err = exec.LookPath(parts[0])
			compilerOK = err == nil
		}
	}
	fmt.Printf("C compiler for race tests: %s\n", capability(compilerOK))
	if gitErr != nil {
		return errors.New("git is required")
	}
	if *integration && !external && !dockerOK {
		return errors.New("integration tests need a running Docker daemon or FORGE_TEST_DATABASE_URL")
	}
	if *race && !compilerOK {
		return errors.New("race tests require a C compiler; install it or run this gate in CI")
	}
	if *containers && (!dockerOK || !composeOK) {
		return errors.New("container checks require a running Docker daemon and Compose")
	}
	return nil
}

func checkGoVersion(running string) (string, error) {
	content, err := os.ReadFile("go.mod")
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(content)) {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "go" {
			continue
		}
		minimum := "go" + fields[1]
		if !goversion.IsValid(minimum) {
			return "", errors.New("invalid Go version in go.mod")
		}
		// Development toolchains do not always have a comparable release
		// version. The go command still enforces the module's go directive.
		if goversion.IsValid(running) && goversion.Compare(running, minimum) < 0 {
			return minimum, fmt.Errorf("%s is older than project minimum %s; install a supported Go release or enable GOTOOLCHAIN=auto", running, minimum)
		}
		return minimum, nil
	}
	return "", errors.New("go.mod has no go directive")
}

func capability(ok bool) string {
	if ok {
		return "available"
	}
	return "unavailable"
}

func probe(argv ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, argv[0], argv[1:]...).Run() //#nosec G204 G702 -- fixed diagnostic commands
}

func execute(env []string, argv ...string) error {
	fmt.Println("$", strings.Join(argv, " "))
	cmd := exec.Command(argv[0], argv[1:]...) //#nosec G204 G702 -- fixed development commands, no shell
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	return nil
}
