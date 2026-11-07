package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLauncherVersionRefs(t *testing.T) {
	for version, want := range map[string]string{
		"v1.3.0":                                            "v1.3.0",
		"v1.3.0-rc.1":                                       "v1.3.0-rc.1",
		"v0.0.0-20261107123456-abcdef012345":                "abcdef012345",
		"v1.3.1-0.20261107123456-abcdef012345":              "abcdef012345",
		"v1.3.0-rc.1.0.20261107123456-abcdef012345":         "abcdef012345",
		"v1.3.1-0.20261107123456-abcdef012345+incompatible": "abcdef012345",
	} {
		if got := versionRef(version); got != want {
			t.Errorf("versionRef(%q) = %q, want %q", version, got, want)
		}
	}
	previous := scaffoldVersion
	scaffoldVersion = "v1.3.0"
	t.Cleanup(func() { scaffoldVersion = previous })
	if got := launcherRef(); got != "v1.3.0" {
		t.Fatalf("release tag lost its prefix: %q", got)
	}
}

func TestInitRecoversAfterAbruptProcessExit(t *testing.T) {
	dir := fixtureRepository(t)
	index := fixtureGit(t, dir, "ls-files", "--stage")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("untracked notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestInitInterruptedProcessHelper$")
	child.Env = append(os.Environ(), "FORGE_INTERRUPTION_FIXTURE="+dir)
	if out, err := child.CombinedOutput(); err == nil {
		t.Fatalf("child should exit before defers run: %s", out)
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 23 {
			t.Fatalf("unexpected child failure: %v\n%s", err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd/forge/init.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fixture did not interrupt after removing the initializer")
	}
	t.Chdir(dir)
	// runInit's recovery entry point must work even though init.go is gone.
	if err := runInit([]string{"-recover"}); err != nil {
		t.Fatal(err)
	}
	if module, _ := os.ReadFile("go.mod"); !strings.Contains(string(module), moduleName) {
		t.Fatalf("original module was not restored: %s", module)
	}
	if _, err := os.Stat("cmd/forge/init.go"); err != nil {
		t.Fatalf("initializer was not restored: %v", err)
	}
	if _, err := os.Stat("introduced.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("initialized output was not removed")
	}
	if notes, _ := os.ReadFile("notes.txt"); string(notes) != "untracked notes" {
		t.Fatal("unrelated untracked file changed")
	}
	if got := fixtureGit(t, dir, "ls-files", "--stage"); got != index {
		t.Fatal("recovery changed the original index")
	}
	if _, err := os.Stat(recoveryDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("successful recovery left an active journal")
	}
}

func TestInitInterruptedProcessHelper(t *testing.T) {
	dir := os.Getenv("FORGE_INTERRUPTION_FIXTURE")
	if dir == "" {
		return
	}
	t.Chdir(dir)
	paths, err := trackedFiles()
	if err != nil {
		t.Fatal(err)
	}
	before, err := readProjectFiles(dir, paths)
	if err != nil {
		t.Fatal(err)
	}
	after := map[string]projectFile{
		"go.mod":         {Data: []byte("module example.com/partial/project\n"), Mode: 0o600},
		"introduced.txt": {Data: []byte("initialized output"), Mode: 0o600},
	}
	if err := saveProjectRecovery(dir, before, after); err != nil {
		t.Fatal(err)
	}
	if err := activateProjectRecovery(dir); err != nil {
		t.Fatal(err)
	}
	if err := writeProjectFiles(dir, after); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove("cmd/forge/init.go"); err != nil {
		t.Fatal(err)
	}
	// An abrupt process exit deliberately bypasses all cleanup defers, just as
	// interruption during publication would. The parent recovers in a new process.
	os.Exit(23)
}

func TestRecoveryContinuesAfterFailuresAndRetainsBackupForRetry(t *testing.T) {
	root := t.TempDir()
	before := map[string]projectFile{
		"a-blocked.txt": {Data: []byte("original A"), Mode: 0o600},
		"b-ok.txt":      {Data: []byte("original B"), Mode: 0o600},
		"c-blocked.txt": {Data: []byte("original C"), Mode: 0o600},
	}
	after := map[string]projectFile{"new.txt": {Data: []byte("new"), Mode: 0o600}}
	if err := saveProjectRecovery(root, before, after); err != nil {
		t.Fatal(err)
	}
	if err := activateProjectRecovery(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a-blocked.txt", "c-blocked.txt"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	err := recoverProject(root)
	if err == nil || !strings.Contains(err.Error(), "a-blocked.txt") || !strings.Contains(err.Error(), "c-blocked.txt") {
		t.Fatalf("recovery should collect every blocked path: %v", err)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "b-ok.txt")); string(content) != "original B" {
		t.Fatal("one unwritable path prevented restoration of other files")
	}
	if _, err := os.Stat(filepath.Join(root, recoveryDirectory, "manifest.json")); err != nil {
		t.Fatalf("failed recovery lost its backup: %v", err)
	}
	for _, name := range []string{"a-blocked.txt", "c-blocked.txt"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := recoverProject(root); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
}

func TestRecoveryRejectsPathsOutsideProjectAndGitMetadata(t *testing.T) {
	for _, path := range []string{".", "file/..", "../outside", ".git/config", recoveryDirectory + "/manifest.json", ".forge-init-backup-other/file"} {
		if err := validateRecoveryPath(path); err == nil {
			t.Errorf("accepted reserved/escaping path %q", path)
		}
	}
}

func TestRecoveryRejectsLinkedMetadataAndOriginals(t *testing.T) {
	for _, name := range []string{"manifest.json", "files"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			before := map[string]projectFile{"original.txt": {Data: []byte("original"), Mode: 0o600}}
			if err := saveProjectRecovery(root, before, nil); err != nil {
				t.Fatal(err)
			}
			if err := activateProjectRecovery(root); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, recoveryDirectory, name)
			target := filepath.Join(t.TempDir(), name)
			if err := os.Rename(path, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := recoverProject(root); err == nil {
				t.Fatal("recovery followed a linked backup path")
			}
			if _, err := os.Stat(filepath.Join(root, "original.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid backup changed the project: %v", err)
			}
		})
	}
}

func TestPreparedBackupDoesNotOverwriteConcurrentEdits(t *testing.T) {
	root := fixtureRepository(t)
	t.Chdir(root)
	paths, err := trackedFiles()
	if err != nil {
		t.Fatal(err)
	}
	before, err := readProjectFiles(root, paths)
	if err != nil {
		t.Fatal(err)
	}
	after := map[string]projectFile{"go.mod": {Data: []byte("module example.com/initialized/app\n"), Mode: 0o600}}
	if err := validateProjectPublication(root, paths, before, after); err != nil {
		t.Fatal(err)
	}
	if err := saveProjectRecovery(root, before, after); err != nil {
		t.Fatal(err)
	}
	if err := saveProjectRecovery(root, before, after); err == nil {
		t.Fatal("a second publisher acquired the active journal")
	}
	concurrent := []byte("module example.com/concurrent/edit\n")
	if err := os.WriteFile("go.mod", concurrent, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateProjectPublication(root, paths, before, after); err == nil {
		t.Fatal("an edit during journal creation was not detected")
	}
	// A crash after the failed recheck leaves a prepared journal. Recovery must
	// discard it without replacing the other agent's newer edit with its snapshot.
	if err := recoverProject(root); err != nil {
		t.Fatal(err)
	}
	if current, _ := os.ReadFile("go.mod"); string(current) != string(concurrent) {
		t.Fatal("unused recovery snapshot overwrote a concurrent edit")
	}
}

func TestRecoveryDirectoriesAreIgnoredButProvenanceIsTracked(t *testing.T) {
	ignore, err := os.ReadFile(filepath.Join(repoRoot(t), ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	dir := fixtureRepository(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), ignore, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".forge-init-recovery/manifest.json", ".forge-init-backup-123/original.txt", ".forge-init-retired-123/file", ".forge-new-123/file"} {
		if got := fixtureGit(t, dir, "check-ignore", path); got != path {
			t.Fatalf("backup path must be ignored: %s", path)
		}
	}
	if err := exec.Command("git", "-C", dir, "check-ignore", "-q", ".forge.json").Run(); err == nil {
		t.Fatal("project provenance must remain committable")
	}
}

func TestProjectModesSurviveRestrictiveUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX creation permissions and umask do not apply on Windows")
	}
	for _, scenario := range []string{"publication", "recovery"} {
		t.Run(scenario, func(t *testing.T) {
			root := fixtureRepository(t)
			for name, file := range projectModeFixtures() {
				path := filepath.Join(root, filepath.FromSlash(name))
				if err := os.WriteFile(path, file.Data, 0o600); err != nil {
					t.Fatal(err)
				}
				// Make setup independent of the parent process's own umask.
				if err := os.Chmod(path, file.Mode); err != nil {
					t.Fatal(err)
				}
			}
			fixtureGit(t, root, "add", "--all")
			index := fixtureGit(t, root, "ls-files", "--stage")
			t.Chdir(root)
			paths, err := trackedFiles()
			if err != nil {
				t.Fatal(err)
			}
			before, err := readProjectFiles(root, paths)
			if err != nil {
				t.Fatal(err)
			}
			// Only the child changes its umask. The test process and any other
			// tests retain their original creation permissions.
			child := exec.Command("/bin/sh", "-c", `umask 077; exec "$@"`, "sh", os.Args[0], "-test.run=^TestProjectModeProcessHelper$")
			child.Env = append(os.Environ(), "FORGE_MODE_FIXTURE="+root, "FORGE_MODE_SCENARIO="+scenario)
			out, err := child.CombinedOutput()
			if scenario == "recovery" {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 23 {
					t.Fatalf("expected interruption after publication: %v\n%s", err, out)
				}
				if err := recoverProject(root); err != nil {
					t.Fatal(err)
				}
				assertProjectModes(t, root, before)
			} else {
				if err != nil {
					t.Fatalf("initialize under restrictive umask: %v\n%s", err, out)
				}
				assertProjectModes(t, root, publishedModeFixtures())
			}
			if current := fixtureGit(t, root, "ls-files", "--stage"); current != index {
				t.Error("initialization or recovery changed the original index")
			}
			if _, err := os.Stat(filepath.Join(root, recoveryDirectory)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("completed operation left an active journal: %v", err)
			}
		})
	}
}

func TestProjectModeProcessHelper(t *testing.T) {
	root := os.Getenv("FORGE_MODE_FIXTURE")
	if root == "" {
		return
	}
	t.Chdir(root)
	if os.Getenv("FORGE_MODE_SCENARIO") == "publication" {
		if err := prepareProject("example.com/team/app", nil, func(_ string, _ map[string]bool) error {
			// Verify actual transformed staging, including a newly named
			// migration whose publication cannot inherit an existing mode.
			assertProjectModes(t, ".", publishedModeFixtures())
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	paths, err := trackedFiles()
	if err != nil {
		t.Fatal(err)
	}
	before, err := readProjectFiles(root, paths)
	if err != nil {
		t.Fatal(err)
	}
	after := make(map[string]projectFile, len(before))
	for name, file := range before {
		after[name] = file
	}
	after["readable.txt"] = projectFile{Data: []byte("partial initialization"), Mode: 0o600}
	delete(after, "script.sh")
	if err := saveProjectRecovery(root, before, after); err != nil {
		t.Fatal(err)
	}
	if err := activateProjectRecovery(root); err != nil {
		t.Fatal(err)
	}
	if err := writeProjectFiles(root, after); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove("script.sh"); err != nil {
		t.Fatal(err)
	}
	// Bypass defers so recovery must use the persisted originals, not memory.
	os.Exit(23)
}

func projectModeFixtures() map[string]projectFile {
	return map[string]projectFile{
		"readable.txt":               {Data: []byte("shared source\n"), Mode: 0o644},
		"script.sh":                  {Data: []byte("#!/bin/sh\nexit 0\n"), Mode: 0o755},
		"migrations/00002_modes.sql": {Data: []byte("-- source migration\n"), Mode: 0o644},
	}
}

func publishedModeFixtures() map[string]projectFile {
	files := projectModeFixtures()
	files["migrations/00001_modes.sql"] = files["migrations/00002_modes.sql"]
	delete(files, "migrations/00002_modes.sql")
	return files
}

func assertProjectModes(t *testing.T, root string, want map[string]projectFile) {
	// Compare actual bytes and modes instead of just invoking the writer again.
	t.Helper()
	for name, expected := range want {
		path := filepath.Join(root, filepath.FromSlash(name))
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, expected.Data) {
			t.Errorf("%s: bytes = %q, error = %v; want %q", name, data, err, expected.Data)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Error(err)
		} else if info.Mode().Perm() != expected.Mode {
			t.Errorf("%s: mode = %04o, want %04o", name, info.Mode().Perm(), expected.Mode)
		}
	}
}
