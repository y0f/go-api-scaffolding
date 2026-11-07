package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionedProjectIdentities(t *testing.T) {
	for _, suffix := range []string{"v2", "v10", "v100"} {
		module := "example.com/team/api/" + suffix
		if projectName(module) != "api" || unversionedModule(module) != "example.com/team/api" {
			t.Fatalf("versioned module identity: %s", module)
		}
		if !strings.HasPrefix(composeProjectName(module), "api-") {
			t.Fatalf("Compose identity used a version as the app name: %s", module)
		}
		if composeProjectName(module) == composeProjectName("example.com/team/api") {
			t.Fatal("distinct module identities must keep distinct Compose volumes")
		}
	}
	for _, module := range []string{"example.com/team/v1", "example.com/team/v02", "example.com/team/v0", "example.com/team/v2alpha", "example.com/team/v2/api"} {
		if unversionedModule(module) != module {
			t.Fatalf("ordinary module path changed: %s", module)
		}
	}
}

func TestRewriteVersionedModulePreservesLibraryVersions(t *testing.T) {
	dir := fixtureRepository(t)
	t.Chdir(dir)
	files := map[string]string{
		"README.md":        "# Go API scaffold for parallel agents\n\nLibrary v2 stays v2.\n",
		"tools/go.mod":     "module " + scaffoldToolsModule + "\n",
		"main.go":          "package main\nimport _ \"" + moduleName + "/migrations\"\nimport _ \"example.com/library/v2\"\n",
		".goreleaser.yaml": "project_name: gophling\nbuilds:\n  - ldflags: -X " + moduleName + "/internal/config.version=example\n",
	}
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, dir, "add", "--all")
	if err := rewriteTree("example.com/team/api/v2", map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	for name, wanted := range map[string][]string{
		"README.md":        {"# api\n", "Library v2 stays v2."},
		"go.mod":           {"module example.com/team/api/v2\n"},
		"tools/go.mod":     {"module example.com/team/api/tools\n"},
		"main.go":          {"example.com/team/api/v2/migrations", "example.com/library/v2"},
		".goreleaser.yaml": {"project_name: \"api\"", "example.com/team/api/v2/internal/config.version"},
	} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wanted {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s missing %q", name, want)
			}
		}
	}
}

func TestVersionedLauncherUsesRepositoryAndReleaseRef(t *testing.T) {
	source := fixtureRepository(t)
	fixtureGit(t, source, "tag", "v2.0.0")
	revision := fixtureGit(t, source, "rev-parse", "HEAD")
	// Redirect only this test's canonical public URL to an owned local fixture.
	// HTTPS is forbidden so an incorrect default cannot contact the network.
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+filepath.ToSlash(source)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/y0f/gophling")
	t.Setenv("GIT_CONFIG_KEY_1", "protocol.https.allow")
	t.Setenv("GIT_CONFIG_VALUE_1", "never")
	previous := scaffoldVersion
	scaffoldVersion = "v2.0.0"
	t.Cleanup(func() { scaffoldVersion = previous })
	destination := filepath.Join(t.TempDir(), "app")
	err := createProject([]string{destination, "-yes", "-module", "example.com/team/api/v2"}, func([]string) error {
		return prepareProject("example.com/team/api/v2", nil, func(string, map[string]bool) error { return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, ".forge.json"))
	if err != nil {
		t.Fatal(err)
	}
	var origin projectOrigin
	if err := json.Unmarshal(data, &origin); err != nil {
		t.Fatal(err)
	}
	if origin.Scaffold != scaffoldRepository || origin.Ref != "v2.0.0" || origin.Revision != revision || origin.Module != "example.com/team/api/v2" {
		t.Fatalf("wrong repository/version provenance: %#v", origin)
	}
	if diff := fixtureGit(t, destination, "diff", "--name-only"); diff != "" {
		t.Fatalf("unstaged source: %s", diff)
	}
}

func TestRejectMismatchedScaffoldBeforeSetup(t *testing.T) {
	dir := fixtureRepository(t)
	t.Chdir(dir)
	if err := os.WriteFile("go.mod", []byte("module github.com/y0f/gophling\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := fixtureGit(t, dir, "status", "--porcelain")
	err := prepareProject("example.com/team/api/v2", nil, func(string, map[string]bool) error {
		t.Fatal("incompatible source reached setup")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "use the launcher from the selected release") {
		t.Fatalf("missing actionable compatibility error: %v", err)
	}
	if after := fixtureGit(t, dir, "status", "--porcelain"); after != before {
		t.Fatal("rejection changed source or index")
	}
}
