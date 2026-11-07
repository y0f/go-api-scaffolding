package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fixtureRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                  "module " + moduleName + "\n\ngo 1.26.0\n",
		"cmd/forge/init.go":       "package main\n",
		"migrations/embed.go":     "package migrations\n",
		"internal/outbox/data.go": "package outbox\n",
		".gitignore":              "/bin/\n",
	}
	for name, data := range files {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, dir, "init", "--quiet")
	fixtureGit(t, dir, "add", "--all")
	fixtureGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "fixture")
	return dir
}

func fixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRewriteTreePreservesRequestedModule(t *testing.T) {
	const module = "example.com/gophling-team/gophling-app"
	dir := fixtureRepository(t)
	t.Chdir(dir)
	files := map[string]string{
		"README.md": "# gophling\n\nRun gophling with " + moduleName + "/cmd/api.\n",
		"main.go":   "package main\nimport _ \"" + moduleName + "/migrations\"\n",
	}
	for name, content := range files {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, dir, "add", "--all")
	if err := rewriteTree(module, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"go.mod":    "module " + module + "\n\ngo 1.26.0\n",
		"README.md": "# gophling-app\n\nRun gophling-app with " + module + "/cmd/api.\n",
		"main.go":   "package main\nimport _ \"" + module + "/migrations\"\n",
	}
	for name, expected := range want {
		actual, err := os.ReadFile(name)
		if err != nil || string(actual) != expected {
			t.Errorf("%s = %q, %v; want %q", name, actual, err, expected)
		}
	}
}

func TestRewriteTreeGoReleaserProjectName(t *testing.T) {
	for _, tc := range []struct {
		name, file, projectName, wantName string
	}{
		{"default", ".goreleaser.yaml", "gophling", `"gophling-app"`},
		{"single_quoted", ".goreleaser.yml", "'gophling'", `"gophling-app"`},
		{"double_quoted_comment", ".goreleaser.yaml", `"gophling" # release name`, `"gophling-app" # release name`},
		{"custom", ".goreleaser.yaml", "my-service", "my-service"},
		{"custom_contains_scaffold", ".goreleaser.yaml", "my-gophling-service", "my-gophling-service"},
		{"custom_quoted", ".goreleaser.yml", `'my-gophling-service' # keep`, `'my-gophling-service' # keep`},
		{"custom_module", ".goreleaser.yaml", `"` + moduleName + `"`, `"` + moduleName + `"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fixtureRepository(t)
			t.Chdir(dir)
			const module = "example.com/gophling-team/gophling-app"
			src := "version: 2\nproject_name: " + tc.projectName + "\nbuilds:\n  - ldflags:\n      - -X " + moduleName + "/internal/config.version={{ .Version }}\n"
			if err := os.WriteFile(tc.file, []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
			fixtureGit(t, dir, "add", tc.file)
			if err := rewriteTree(module, map[string]bool{}); err != nil {
				t.Fatal(err)
			}
			want := "version: 2\nproject_name: " + tc.wantName + "\nbuilds:\n  - ldflags:\n      - -X " + module + "/internal/config.version={{ .Version }}\n"
			got, err := os.ReadFile(tc.file)
			if err != nil || string(got) != want {
				t.Fatalf("release config = %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestCreateProjectPinsSourceAndStagesFinalTree(t *testing.T) {
	source := fixtureRepository(t)
	revision := fixtureGit(t, source, "rev-parse", "HEAD")
	fixtureGit(t, source, "tag", "v0.1.0")
	if err := os.WriteFile(filepath.Join(source, "later.txt"), []byte("newer branch content"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, source, "add", "--all")
	fixtureGit(t, source, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "later")
	destination := filepath.Join(t.TempDir(), "app")
	initialCWD, _ := os.Getwd()
	initialize := func(_ []string) error {
		if _, err := os.Stat("later.txt"); !errors.Is(err, os.ErrNotExist) {
			t.Error("cloned HEAD instead of the selected tag")
		}
		if err := os.WriteFile("go.mod", []byte("module example.com/team/app\n"), 0o600); err != nil {
			return err
		}
		if err := os.Remove("cmd/forge/init.go"); err != nil {
			return err
		}
		return os.WriteFile("new.txt", []byte("new project file"), 0o600)
	}
	if err := createProject([]string{destination, "-source", source, "-ref", "v0.1.0", "-yes", "-module", "example.com/team/app"}, initialize); err != nil {
		t.Fatal(err)
	}
	if cwd, _ := os.Getwd(); cwd != initialCWD {
		t.Fatalf("cwd changed: %s", cwd)
	}
	if diff := fixtureGit(t, destination, "diff", "--name-only"); diff != "" {
		t.Fatalf("transformation was not staged: %s", diff)
	}
	if staged := fixtureGit(t, destination, "show", ":go.mod"); !strings.Contains(staged, "example.com/team/app") {
		t.Fatalf("index contains template module: %s", staged)
	}
	if got := fixtureGit(t, destination, "show", ":new.txt"); got != "new project file" {
		t.Fatalf("new file not staged: %s", got)
	}
	var origin projectOrigin
	data, err := os.ReadFile(filepath.Join(destination, ".forge.json"))
	if err != nil || json.Unmarshal(data, &origin) != nil {
		t.Fatalf("provenance: %s, %v", data, err)
	}
	if origin.Revision != revision || origin.Ref != "v0.1.0" {
		t.Fatalf("provenance = %#v", origin)
	}
	if strings.Contains(string(data), source) || origin.Scaffold != moduleName {
		t.Fatal("public provenance retained a machine-local clone source")
	}
	fixtureGit(t, destination, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "initial")
	if status := fixtureGit(t, destination, "status", "--porcelain"); status != "" {
		t.Fatalf("first commit left changes: %s", status)
	}
}

func TestCreateProjectFailureLeavesDestinationAbsent(t *testing.T) {
	source := fixtureRepository(t)
	destination := filepath.Join(t.TempDir(), "app")
	failure := errors.New("tool unavailable")
	err := createProject([]string{destination, "-source", source, "-yes", "-module", "example.com/team/app"}, func(_ []string) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed creation left destination: %v", err)
	}
}

func TestPrepareProjectFailurePreservesOriginalAndIndex(t *testing.T) {
	dir := fixtureRepository(t)
	t.Chdir(dir)
	before := fixtureGit(t, dir, "ls-files", "--stage")
	failure := errors.New("generator failed")
	err := prepareProject("example.com/team/app", map[string]bool{"outbox": true}, func(_ string, _ map[string]bool) error {
		if _, err := os.Stat("cmd/forge/init.go"); !errors.Is(err, os.ErrNotExist) {
			t.Error("staged tree was not transformed")
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	if got := fixtureGit(t, dir, "ls-files", "--stage"); got != before {
		t.Error("index changed on failed setup")
	}
	if got := fixtureGit(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("working tree changed on failed setup: %s", got)
	}
}

func TestPrepareProjectPublishesVerifiedFilesAndPreservesLocalFiles(t *testing.T) {
	dir := fixtureRepository(t)
	t.Chdir(dir)
	if err := os.WriteFile("notes.txt", []byte("local notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := prepareProject("example.com/team/app", map[string]bool{"outbox": true}, func(_ string, _ map[string]bool) error {
		return os.WriteFile("verified.txt", []byte("checked"), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile("go.mod"); !strings.Contains(string(data), "example.com/team/app") {
		t.Fatalf("module not published: %s", data)
	}
	for _, name := range []string{"verified.txt", "notes.txt", ".forge.json"} {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	if _, err := os.Stat("internal/outbox"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dropped package survived: %v", err)
	}
	if data := fixtureGit(t, dir, "show", ":go.mod"); !strings.Contains(data, moduleName) {
		t.Error("in-place init changed the user's index")
	}
}

func TestPrepareProjectRejectsUntrackedOutputCollision(t *testing.T) {
	dir := fixtureRepository(t)
	t.Chdir(dir)
	if err := os.WriteFile("verified.txt", []byte("local"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := prepareProject("example.com/team/app", nil, func(_ string, _ map[string]bool) error {
		return os.WriteFile("verified.txt", []byte("generated"), 0o600)
	})
	if err == nil || !strings.Contains(err.Error(), "untracked") {
		t.Fatalf("error = %v", err)
	}
	if data, _ := os.ReadFile("verified.txt"); string(data) != "local" {
		t.Error("untracked file overwritten")
	}
}

func TestPrepareProjectPreservesEditsMadeDuringVerification(t *testing.T) {
	dir := fixtureRepository(t)
	t.Chdir(dir)
	edit := []byte("module example.com/local/edit\n")
	err := prepareProject("example.com/team/app", nil, func(_ string, _ map[string]bool) error {
		return os.WriteFile(filepath.Join(dir, "go.mod"), edit, 0o600)
	})
	if err == nil || !strings.Contains(err.Error(), "changed during setup") {
		t.Fatalf("error = %v", err)
	}
	if data, _ := os.ReadFile("go.mod"); string(data) != string(edit) {
		t.Error("setup overwrote an edit made during verification")
	}
	if _, err := os.Stat("cmd/forge/init.go"); err != nil {
		t.Fatalf("initializer should remain available: %v", err)
	}
}

func TestPrepareProjectRejectsNewTrackedFilesDuringVerification(t *testing.T) {
	dir := fixtureRepository(t)
	t.Chdir(dir)
	err := prepareProject("example.com/team/app", nil, func(_ string, _ map[string]bool) error {
		if err := os.WriteFile(filepath.Join(dir, "concurrent.go"), []byte("package main\n"), 0o600); err != nil {
			return err
		}
		fixtureGit(t, dir, "add", "concurrent.go")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "tracked project files changed") {
		t.Fatalf("new tracked input should abort publication: %v", err)
	}
	if _, err := os.Stat("cmd/forge/init.go"); err != nil {
		t.Fatalf("initializer changed despite concurrent modification: %v", err)
	}
	if data := fixtureGit(t, dir, "show", ":concurrent.go"); data != "package main" {
		t.Fatal("other agent's staged file changed")
	}
}

func TestLocalTaskCommandDoesNotRequirePATH(t *testing.T) {
	want := "./bin/task"
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got := localTaskCommand(); got != want {
		t.Fatalf("task command = %q, want %q", got, want)
	}
}

func TestPrepareProjectPreservesIntentionallyTrackedIgnoredFiles(t *testing.T) {
	dir := fixtureRepository(t)
	t.Chdir(dir)
	if err := os.WriteFile(".gitignore", []byte("/bin/\n/kept.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("kept.txt", []byte("intentionally tracked"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, dir, "add", "--force", "kept.txt", ".gitignore")
	if err := prepareProject("example.com/team/app", nil, func(_ string, _ map[string]bool) error {
		if got := fixtureGit(t, ".", "ls-files", "kept.txt"); got != "kept.txt" {
			t.Fatal("staging dropped an intentionally tracked ignored source")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile("kept.txt"); string(content) != "intentionally tracked" {
		t.Fatal("init deleted an intentionally tracked file")
	}
}

func TestPrepareProjectKeepsProvenanceDespiteIgnoreRules(t *testing.T) {
	dir := fixtureRepository(t)
	t.Chdir(dir)
	if err := os.WriteFile(".gitignore", []byte("/bin/\n.forge.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	index := fixtureGit(t, dir, "ls-files", "--stage")
	if err := prepareProject("example.com/team/app", nil, func(_ string, _ map[string]bool) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var origin projectOrigin
	content, err := os.ReadFile(".forge.json")
	if err != nil || json.Unmarshal(content, &origin) != nil {
		t.Fatalf("required project provenance was not published: %s, %v", content, err)
	}
	if origin.Module != "example.com/team/app" || origin.Revision == "" {
		t.Fatalf("incomplete project provenance: %#v", origin)
	}
	if current := fixtureGit(t, dir, "ls-files", "--stage"); current != index {
		t.Fatal("publishing provenance changed the original index")
	}
}
