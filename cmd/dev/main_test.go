package main

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedCheckIncludesEveryWorkingTreeState(t *testing.T) {
	for _, state := range []string{"tracked", "staged", "untracked"} {
		t.Run(state, func(t *testing.T) {
			t.Chdir(gitFixtureDir(t))
			git := func(args ...string) {
				t.Helper()
				// Tiny owned fixtures need no asynchronous Git maintenance.
				args = append([]string{"-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)
				if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			git("init", "--quiet")
			writeFixture(t, "internal/gen/api.go", "original")
			if state != "untracked" {
				git("add", "internal/gen")
				git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "fixture")
			}
			if state == "staged" {
				writeFixture(t, "internal/gen/api.go", "staged stale output")
				git("add", "internal/gen")
			}
			regenerate := func() error {
				return os.WriteFile("internal/gen/api.go", []byte("correct output"), 0o600)
			}
			if err := checkGenerated("internal/gen", regenerate); err == nil || !strings.Contains(err.Error(), "api.go") {
				t.Fatalf("stale %s output was not reported: %v", state, err)
			}
			if err := checkGenerated("internal/gen", regenerate); err != nil {
				t.Fatalf("valid uncommitted output must pass: %v", err)
			}
		})
	}
}

func gitFixtureDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	// Git objects are read-only. Some Windows sandboxes require clearing that
	// attribute explicitly before testing's TempDir cleanup can remove them.
	t.Cleanup(func() {
		if err := makeFixtureWritable(directory); err != nil {
			t.Errorf("prepare owned Git fixture for cleanup: %v", err)
		}
	})
	return directory
}

func makeFixtureWritable(directory string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fixtureCleanupError(err)
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		return makeFixtureEntryWritable(root, name, entry, walkErr)
	})
}

func makeFixtureEntryWritable(root *os.Root, name string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return fixtureCleanupError(walkErr)
	}
	// WalkDir does not follow links; change only regular files in this root.
	if !entry.Type().IsRegular() {
		return nil
	}
	info, err := entry.Info()
	if err != nil {
		return fixtureCleanupError(err)
	}
	return fixtureCleanupError(root.Chmod(name, info.Mode().Perm()|0o600))
}

func fixtureCleanupError(err error) error {
	// Git can remove transient files between enumeration, Info and Chmod.
	// Absence already satisfies cleanup; permission and confinement errors do not.
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

type fixtureEntry struct {
	fs.DirEntry
	readInfo func() (fs.FileInfo, error)
}

func (entry fixtureEntry) Info() (fs.FileInfo, error) { return entry.readInfo() }

func TestGitFixtureCleanupToleratesDisappearingFiles(t *testing.T) {
	for _, stage := range []string{"walk", "info", "chmod"} {
		t.Run(stage, func(t *testing.T) {
			directory := gitFixtureDir(t)
			writeFixture(t, filepath.Join(directory, "maintenance.lock"), "transient")
			root, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			entries, err := fs.ReadDir(root.FS(), ".")
			if err != nil {
				t.Fatal(err)
			}
			entry := entries[0]
			var walkErr error
			switch stage {
			case "walk":
				walkErr = &fs.PathError{Op: "readdir", Path: entry.Name(), Err: fs.ErrNotExist}
			case "info":
				if err := root.Remove(entry.Name()); err != nil {
					t.Fatal(err)
				}
				original := entry
				// Force a fresh stat: Windows directory entries may cache Info.
				entry = fixtureEntry{DirEntry: original, readInfo: func() (fs.FileInfo, error) {
					return root.Stat(original.Name())
				}}
			case "chmod":
				original := entry
				entry = fixtureEntry{DirEntry: original, readInfo: func() (fs.FileInfo, error) {
					info, infoErr := original.Info()
					if infoErr != nil {
						return nil, infoErr
					}
					// Reproduce Git removing its lock after Info, before Chmod.
					if err := root.Remove(original.Name()); err != nil {
						t.Fatal(err)
					}
					return info, nil
				}}
			}
			if err := makeFixtureEntryWritable(root, entry.Name(), entry, walkErr); err != nil {
				t.Fatalf("file disappearing during %s must not fail cleanup: %v", stage, err)
			}
		})
	}
}

func TestGitFixtureCleanupPreservesOtherErrors(t *testing.T) {
	directory := gitFixtureDir(t)
	writeFixture(t, filepath.Join(directory, "object"), "owned fixture")
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		t.Fatal(err)
	}
	entry := entries[0]
	if err := makeFixtureEntryWritable(root, entry.Name(), entry, fs.ErrPermission); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("lost directory traversal error: %v", err)
	}
	unreadable := fixtureEntry{DirEntry: entry, readInfo: func() (fs.FileInfo, error) { return nil, fs.ErrPermission }}
	if err := makeFixtureEntryWritable(root, entry.Name(), unreadable, nil); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("lost file metadata error: %v", err)
	}
	if err := makeFixtureEntryWritable(root, filepath.Join("..", "outside"), entry, nil); err == nil {
		t.Fatal("cleanup suppressed an attempted root escape")
	}
}

func TestGitFixtureCleanupAlreadyRemoved(t *testing.T) {
	if err := makeFixtureWritable(filepath.Join(t.TempDir(), "already-removed")); err != nil {
		t.Fatalf("absent fixture needs no cleanup: %v", err)
	}
}

func TestGitFixtureCleanupMakesReadOnlyObjectsRemovable(t *testing.T) {
	directory := gitFixtureDir(t)
	object := filepath.Join(directory, "objects", "object")
	writeFixture(t, object, "owned fixture")
	if err := os.Chmod(object, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := makeFixtureWritable(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(object)); err != nil {
		t.Fatalf("read-only fixture remains undeletable: %v", err)
	}
}

func TestGitFixtureCleanupDoesNotFollowSymlinks(t *testing.T) {
	directory := gitFixtureDir(t)
	outside := filepath.Join(t.TempDir(), "outside")
	writeFixture(t, outside, "not owned by fixture")
	if err := os.Chmod(outside, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(outside, 0o600); err != nil {
			t.Error(err)
		}
	})
	if err := os.Symlink(outside, filepath.Join(directory, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := makeFixtureWritable(directory); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o200 != 0 {
		t.Fatal("cleanup changed the symlink target outside the fixture")
	}
}

func TestGeneratedCheckDetectsAddedAndDeletedEmptyFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "internal/gen/deleted.go", "")
	err := checkGenerated("internal/gen", func() error {
		if err := os.Remove("internal/gen/deleted.go"); err != nil {
			return err
		}
		return os.WriteFile("internal/gen/new.go", nil, 0o600)
	})
	if err == nil || !strings.Contains(err.Error(), "deleted.go") || !strings.Contains(err.Error(), "new.go") {
		t.Fatalf("expected both changed paths: %v", err)
	}
}

func TestGeneratedCheckMissingDirectoryAndGeneratorFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	want := errors.New("generator failed")
	if err := checkGenerated("missing", func() error { return want }); !errors.Is(err, want) {
		t.Fatalf("got %v, want generator failure", err)
	}
	if err := checkGenerated("missing", func() error {
		writeFixture(t, "missing/new.go", "generated")
		return nil
	}); err == nil {
		t.Fatal("new generated directory must be reported")
	}
}

func TestToolsStampTracksPinnedInputs(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "go.mod", "module example.com/app")
	writeFixture(t, "go.sum", "application checksum")
	writeFixture(t, "tools/go.mod", "toolchain go1.27.1")
	writeFixture(t, "tools/go.sum", "first checksum")
	before, err := toolsStamp()
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, "tools/go.sum", "updated checksum")
	after, err := toolsStamp()
	if err != nil {
		t.Fatal(err)
	}
	if string(before) == string(after) {
		t.Fatal("changed pinned dependencies must invalidate the tools cache")
	}
	writeFixture(t, "go.sum", "updated application checksum")
	updatedApp, err := toolsStamp()
	if err != nil {
		t.Fatal(err)
	}
	if string(after) == string(updatedApp) {
		t.Fatal("changed application dependencies must be downloaded by bootstrap")
	}
	if toolsExist() {
		t.Fatal("a partial or absent toolchain must be rebuilt")
	}
}

func writeFixture(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateRejectsConflictingBranchMigrationsBeforeRunningTools(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "migrations/00007_orders.sql", "-- first branch")
	writeFixture(t, "migrations/7_notes.sql", "-- second branch")
	err := generate()
	if err == nil || !strings.Contains(err.Error(), "duplicate migration version 7") {
		t.Fatalf("got %v, want migration conflict before missing tools", err)
	}
	if !strings.Contains(err.Error(), "00007_orders.sql") || !strings.Contains(err.Error(), "7_notes.sql") {
		t.Fatalf("conflict must identify both branches' files: %v", err)
	}
}

func TestMigrationVersionsAllowGapsAndEmptyProjects(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("migrations", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := checkMigrationVersions("migrations"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"00001_initial.sql", "00009_feature.sql", "embed.go", "README.md"} {
		writeFixture(t, filepath.Join("migrations", name), "")
	}
	if err := checkMigrationVersions("migrations"); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorChecksTheProjectGoMinimum(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFixture(t, "go.mod", "module example.com/app\n\ngo 1.26.10\ntoolchain go1.27.3\n")
	for _, running := range []string{"go1.26.0", "go1.26.9", "go1.25.10"} {
		if _, err := checkGoVersion(running); err == nil {
			t.Errorf("accepted outdated %s", running)
		}
	}
	for _, running := range []string{"go1.26.10", "go1.27.3", "go1.28.0", "devel go1.28-abcdef"} {
		minimum, err := checkGoVersion(running)
		if err != nil || minimum != "go1.26.10" {
			t.Errorf("%s: minimum %q, error %v", running, minimum, err)
		}
	}
}
