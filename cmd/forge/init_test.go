package main

import (
	"os"
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
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
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
