package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mark builds a marker line without spelling one out: this file is itself
// tracked, so TestScaffoldMarkers would otherwise read these as real markers.
func mark(prefix, kind, name string) string {
	return prefix + " forge:" + kind + " " + name
}

func TestStripRemovesDroppedBlocksAndMarkers(t *testing.T) {
	src := strings.Join([]string{
		"keep 1",
		mark("//", "begin", "outbox"),
		"outbox only",
		mark("//", "end", "outbox"),
		"",
		mark("#", "begin", "admin"),
		"admin stays, marker goes",
		mark("#", "end", "admin"),
		"keep 2",
		"",
	}, "\n")
	got, err := strip(src, map[string]bool{"outbox": true})
	if err != nil {
		t.Fatal(err)
	}
	want := "keep 1\n\nadmin stays, marker goes\nkeep 2\n"
	if got != want {
		t.Fatalf("strip =\n%q\nwant\n%q", got, want)
	}
}

func TestStripRejectsBrokenMarkers(t *testing.T) {
	for _, src := range []string{
		mark("//", "begin", "outbox") + "\nx\n",
		"x\n" + mark("//", "end", "outbox") + "\n",
		mark("//", "begin", "outbox") + "\n" + mark("//", "end", "admin") + "\n",
		mark("//", "begin", "nosuch") + "\n" + mark("//", "end", "nosuch") + "\n",
	} {
		if _, err := strip(src, nil); err == nil {
			t.Errorf("strip(%q) accepted a broken marker", src)
		}
	}
}

// TestScaffoldMarkers runs strip over every tracked file exactly as init does,
// so an unbalanced or misspelled marker anywhere in the repository fails here
// rather than on someone's first `forge init`. It also checks that every slice
// still owns at least one path or block, so a slice cannot silently become a
// no-op as the code evolves.
func TestScaffoldMarkers(t *testing.T) {
	root := repoRoot(t)
	t.Chdir(root)
	files, err := trackedFiles()
	if err != nil {
		t.Fatal(err)
	}
	referenced := map[string]bool{}
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.IndexByte(string(src), 0) >= 0 {
			continue
		}
		if _, err := strip(string(src), nil); err != nil {
			t.Errorf("%s: %v", path, err)
		}
		for _, m := range markerRe.FindAllStringSubmatch(string(src), -1) {
			referenced[m[2]] = true
		}
	}
	for _, s := range slices {
		for _, p := range s.Paths {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("slice %s: path %s does not exist", s.Name, p)
			}
		}
		if !referenced[s.Name] {
			t.Errorf("slice %s has no marker block anywhere", s.Name)
		}
	}
	for _, d := range derived {
		for _, p := range d.Paths {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("derived slice %s: path %s does not exist", d.Name, p)
			}
		}
		if !referenced[d.Name] && len(d.Paths) == 0 {
			t.Errorf("derived slice %s owns nothing", d.Name)
		}
	}
	if !referenced["init"] {
		t.Error("no init block: the installer would not remove its own wiring")
	}
}

// Files that describe the scaffold rather than the project it generates go
// with the installer. The rewrite runs on a copy of those files plus one the
// project keeps, staged in a fresh repository as forge new does.
func TestInitRemovesTemplateOnlyFiles(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	for _, p := range append([]string{"README.md", "CHANGELOG.md", "LICENSE", ".goreleaser.yaml"}, initPaths...) {
		if p == "evals" || p == "docs/_layouts" {
			if err := os.MkdirAll(filepath.Join(dir, p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, p, "README.md"), []byte("scaffold evaluations"), 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if errors.Is(err, os.ErrNotExist) {
			src = []byte("scaffold-only fixture\n")
		} else if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, src, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "migrations"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, step := range [][]string{{"git", "init", "--quiet"}, {"git", "add", "--all"}} {
		if out, err := exec.Command(step[0], step[1:]...).CombinedOutput(); err != nil { //#nosec G204 -- fixed argv
			t.Fatalf("%v: %v\n%s", step, err, out)
		}
	}
	if err := rewriteTree("github.com/you/app", map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range append([]string{"CHANGELOG.md"}, initPaths...) {
		if _, err := os.Stat(filepath.FromSlash(p)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived forge init (stat err %v)", p, err)
		}
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("README.md was removed: %v", err)
	}
	if strings.Contains(string(readme), moduleName) {
		t.Error("README.md kept the scaffold module path")
	}
	if !strings.HasPrefix(string(readme), "# app\n") {
		t.Error("README.md must use the initialized project's name as its title")
	}
	release, err := os.ReadFile(".goreleaser.yaml")
	if err != nil || strings.Contains(string(release), "CHANGELOG.md") {
		t.Fatalf("initialized release config retains a removed scaffold changelog: %s, %v", release, err)
	}
	license, err := os.ReadFile("LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	originalLicense, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	for _, notice := range copyrightRe.FindAllString(string(originalLicense), -1) {
		if !strings.Contains(string(license), notice) {
			t.Errorf("initialized license removed original notice %q", notice)
		}
	}
	notices := copyrightRe.FindAllString(string(license), -1)
	if len(notices) == 0 || !strings.HasSuffix(notices[len(notices)-1], " you") {
		t.Fatalf("initialized license lacks new module owner's notice: %s", license)
	}
}

func TestRewriteLicensePreservesNoticesAndAddsModuleOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "LICENSE")
	src := "MIT License\n\nCopyright (c) 2020 someone\n\nPermission is hereby granted"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rewriteLicense(path, "github.com/you/app", 2031); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "MIT License\n\nCopyright (c) 2020 someone\nCopyright (c) 2031 you\n\nPermission is hereby granted"
	if string(got) != want {
		t.Fatalf("LICENSE =\n%q\nwant\n%q", got, want)
	}
	if err := rewriteLicense(path, "github.com/you/app", 2031); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("repeated rewrite changed existing notices: %q, %v", got, err)
	}
	if err := rewriteLicense(filepath.Join(t.TempDir(), "missing"), "github.com/you/app", 2031); err != nil {
		t.Fatalf("missing LICENSE should be skipped, got %v", err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rewriteLicense(path, "app", 2031); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if !strings.Contains(string(got), "Copyright (c) 2031 app") {
		t.Fatalf("a module path without a slash should use the whole path as owner, got %q", got)
	}
}

func TestRewriteLicenseKeepsAllOriginalNoticesAndLineEndings(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		path := filepath.Join(t.TempDir(), "LICENSE")
		src := strings.Join([]string{
			"MIT License", "", "Copyright (c) 2020 original", "Copyright (c) 2022 contributor",
			"", "Permission notice and terms remain unchanged.", "",
		}, newline)
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := rewriteLicense(path, "example.com/new-owner/app", 2031); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		added := newline + "Copyright (c) 2031 new-owner"
		if strings.Count(string(got), added) != 1 || strings.Replace(string(got), added, "", 1) != src {
			t.Fatalf("original license changed for newline %q: %q", newline, got)
		}
	}

	path := filepath.Join(t.TempDir(), "LICENSE")
	const custom = "Custom license with no recognized copyright line.\n"
	if err := os.WriteFile(path, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rewriteLicense(path, "example.com/new-owner/app", 2031); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != custom {
		t.Fatalf("custom license changed: %q, %v", got, err)
	}
}

func TestRenumberMigrationsClosesGaps(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"00002_outbox.sql", "00003_idempotency.sql", "00005_create_orders.sql", "embed.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := renumberMigrations(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	want := "00001_outbox.sql 00002_idempotency.sql 00003_create_orders.sql embed.go"
	if strings.Join(got, " ") != want {
		t.Fatalf("migrations = %q, want %q", strings.Join(got, " "), want)
	}
	body, err := os.ReadFile(filepath.Join(dir, "00003_create_orders.sql"))
	if err != nil || string(body) != "00005_create_orders.sql" {
		t.Fatalf("renamed file lost its content: %q, %v", body, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
