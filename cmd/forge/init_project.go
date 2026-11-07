package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	stdslices "slices"
	"strings"
)

// Set by release builds. go run module@version supplies Main.Version instead.
var scaffoldVersion string

// Repository identity and product naming are independent of Go major versions.
const (
	scaffoldRepository  = "github.com/y0f/gophling"
	scaffoldName        = "gophling"
	scaffoldToolsModule = "github.com/y0f/gophling/tools"
)

var pseudoVersionRevision = regexp.MustCompile(`[.-][0-9]{14}-([0-9a-f]{12})(?:\+incompatible)?$`)

type projectOrigin struct {
	Scaffold string   `json:"scaffold"`
	Revision string   `json:"revision"`
	Ref      string   `json:"ref,omitempty"`
	Module   string   `json:"module,omitempty"`
	Slices   []string `json:"slices,omitempty"`
}

type projectFile struct {
	Data []byte
	Mode os.FileMode
}

func newProject(args []string) error {
	return createProject(args, runInit)
}

func createProject(args []string, initialize func([]string) error) (result error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: forge new <dir> [-ref tag-or-commit] [-source git-url-or-path] [-yes -module path -drop slices]")
	}
	fs := flag.NewFlagSet("forge new", flag.ContinueOnError)
	source := fs.String("source", "https://"+scaffoldRepository, "scaffold repository")
	ref := fs.String("ref", launcherRef(), "scaffold tag or commit (defaults to the launcher version when available)")
	module := fs.String("module", "", "module path for the project")
	drop := fs.String("drop", "", "comma-separated slices to remove")
	yes := fs.Bool("yes", false, "do not prompt (requires -module)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *yes {
		if err := validateModule(*module); err != nil {
			return err
		}
	}
	if strings.HasPrefix(*ref, "-") || strings.ContainsAny(*ref, "\x00\r\n") {
		return errors.New("invalid scaffold ref")
	}
	destination, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("destination must not exist: %s", destination)
	}
	// Keep staging on the destination filesystem so publication is one rename.
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".forge-new-*")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(stage)) }()
	project := filepath.Join(stage, "project")
	if err := execStep("git", "clone", "--quiet", "--", *source, project); err != nil {
		return err
	}
	original, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(project); err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.Chdir(original)) }()
	if *ref != "" {
		if err := execStep("git", "checkout", "--quiet", "--detach", *ref); err != nil {
			return err
		}
	}
	revision, err := gitOutput("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if err := os.RemoveAll(".git"); err != nil {
		return err
	}
	if err := writeOrigin(projectOrigin{Scaffold: scaffoldRepository, Revision: revision, Ref: *ref}); err != nil {
		return err
	}
	if err := execStep("git", "init", "--quiet"); err != nil {
		return err
	}
	// A fresh clone contains only source files. Preserve files intentionally
	// tracked upstream even if repository or global ignore rules match them.
	if err := execStep("git", "add", "--force", "--all"); err != nil {
		return err
	}
	initArgs := []string{"-module", *module, "-drop", *drop}
	if *yes {
		initArgs = append(initArgs, "-yes")
	}
	previousViaNew := viaNew
	viaNew = true
	defer func() { viaNew = previousViaNew }()
	if err := initialize(initArgs); err != nil {
		return err
	}
	// The first commit must contain the transformed project, not its template.
	if err := execStep("git", "add", "--all"); err != nil {
		return err
	}
	if err := os.Chdir(original); err != nil {
		return err
	}
	if err := os.Rename(project, destination); err != nil {
		return err
	}
	fmt.Printf("\nCreated %s from %s.\nNext: change into %s, review git diff --cached, then commit.\nRun go run ./cmd/dev bootstrap, then %s up.\n", destination, revision, args[0], localTaskCommand())
	return nil
}

func localTaskCommand() string {
	if runtime.GOOS == "windows" {
		return "./bin/task.exe"
	}
	return "./bin/task"
}

func launcherRef() string {
	if scaffoldVersion != "" {
		return versionRef(scaffoldVersion)
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return versionRef(info.Main.Version)
	}
	return ""
}

func versionRef(version string) string {
	// go run module@SHA records a module pseudo-version rather than a Git tag.
	// Its revision suffix is the commit that Git can actually check out.
	if match := pseudoVersionRevision.FindStringSubmatch(version); match != nil {
		return match[1]
	}
	return version
}

func gitOutput(args ...string) (string, error) {
	out, err := exec.Command("git", args...).CombinedOutput() //#nosec G204 -- callers supply fixed git operations, not shell commands
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func writeOrigin(origin projectOrigin) error {
	out, err := json.MarshalIndent(origin, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(".forge.json", append(out, '\n'), 0o600)
}

func prepareProject(module string, dropped map[string]bool, verify func(string, map[string]bool) error) (result error) {
	if sourceModule := projectModule(); sourceModule != moduleName {
		return fmt.Errorf("scaffold module %q does not match launcher module %q; use the launcher from the selected release or select a v2 scaffold ref", sourceModule, moduleName)
	}
	if dropped == nil {
		dropped = map[string]bool{}
	}
	original, err := os.Getwd()
	if err != nil {
		return err
	}
	files, err := trackedFiles()
	if err != nil {
		return err
	}
	before, err := readProjectFiles(original, files)
	if err != nil {
		return err
	}
	origin := projectOrigin{Scaffold: scaffoldRepository, Module: module}
	if existing, ok := before[".forge.json"]; ok {
		if err := json.Unmarshal(existing.Data, &origin); err != nil {
			return fmt.Errorf("read scaffold provenance: %w", err)
		}
	} else if origin.Revision, err = gitOutput("rev-parse", "HEAD"); err != nil {
		return err
	}
	origin.Module = module
	origin.Slices = nil
	for _, slice := range slices {
		if !dropped[slice.Name] {
			origin.Slices = append(origin.Slices, slice.Name)
		}
	}
	stage, err := os.MkdirTemp("", "forge-init-*")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(stage)) }()
	if err := writeProjectFiles(stage, before); err != nil {
		return err
	}
	if err := os.Chdir(stage); err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.Chdir(original)) }()
	for _, step := range [][]string{{"git", "init", "--quiet"}, {"git", "add", "--force", "--all"}} {
		if err := execStep(step...); err != nil {
			return err
		}
	}
	if err := rewriteTree(module, dropped); err != nil {
		return err
	}
	if err := verify(module, dropped); err != nil {
		return fmt.Errorf("setup failed; original project unchanged: %w", err)
	}
	if err := writeOrigin(origin); err != nil {
		return err
	}
	// Provenance is required project source even when a repository or global
	// ignore pattern matches dotfiles. The caller's original index is untouched.
	if err := execStep("git", "add", "--force", "--", ".forge.json"); err != nil {
		return err
	}
	if err := execStep("git", "add", "--all"); err != nil {
		return err
	}
	afterPaths, err := trackedFiles()
	if err != nil {
		return err
	}
	after, err := readProjectFiles(stage, afterPaths)
	if err != nil {
		return err
	}
	if err := os.Chdir(original); err != nil {
		return err
	}
	if err := validateProjectPublication(original, files, before, after); err != nil {
		return err
	}
	if err := saveProjectRecovery(original, before, after); err != nil {
		return err
	}
	// The fixed recovery-directory name excludes another publisher. Recheck
	// after claiming it: another init or editor may have changed the project
	// during backup preparation. An unactivated journal never restores files.
	if err := validateProjectPublication(original, files, before, after); err != nil {
		return errors.Join(err, discardProjectRecovery(original))
	}
	if err := activateProjectRecovery(original); err != nil {
		return err
	}
	if err := replaceProjectFiles(original, before, after); err != nil {
		rollback := recoverProject(original)
		return errors.Join(fmt.Errorf("publish project: %w", err), rollback)
	}
	if err := discardProjectRecovery(original); err != nil {
		return fmt.Errorf("project initialized but could not remove recovery backup: %w", err)
	}
	return nil
}

func validateProjectPublication(root string, paths []string, before, after map[string]projectFile) error {
	currentPaths, err := trackedFiles()
	if err != nil {
		return err
	}
	if !stdslices.Equal(paths, currentPaths) {
		return errors.New("tracked project files changed during setup; rerun init")
	}
	current, err := readProjectFiles(root, paths)
	if err != nil {
		return fmt.Errorf("project changed during setup: %w", err)
	}
	for name, file := range before {
		if now := current[name]; !bytes.Equal(now.Data, file.Data) || now.Mode != file.Mode {
			return fmt.Errorf("project changed during setup: %s; rerun init", name)
		}
	}
	for name := range after {
		if _, tracked := before[name]; !tracked {
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("setup would overwrite an untracked path: %s", name)
			}
		}
	}
	return nil
}

func readProjectFiles(root string, names []string) (map[string]projectFile, error) {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	files := make(map[string]projectFile, len(names))
	for _, name := range names {
		if !filepath.IsLocal(name) {
			return nil, fmt.Errorf("invalid project path: %s", name)
		}
		file := filepath.FromSlash(name)
		info, err := directory.Lstat(file)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("expected a regular project file: %s", name)
		}
		data, err := directory.ReadFile(file)
		if err != nil {
			return nil, err
		}
		files[name] = projectFile{Data: data, Mode: info.Mode().Perm()}
	}
	return files, nil
}

func writeProjectFiles(root string, files map[string]projectFile) error {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	for name, file := range files {
		if !filepath.IsLocal(name) {
			return fmt.Errorf("invalid project path: %s", name)
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := checkProjectParents(root, path); err != nil {
			return err
		}
		localPath := filepath.FromSlash(name)
		if err := directory.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
			return err
		}
		if info, err := directory.Lstat(localPath); err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("expected a regular project file: %s", name)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := directory.WriteFile(localPath, file.Data, file.Mode); err != nil {
			return err
		}
		// Preserve source modes in staging and publication despite umask.
		if err := directory.Chmod(localPath, file.Mode); err != nil {
			return err
		}
	}
	return nil
}

func checkProjectParents(root, file string) error {
	for dir := filepath.Dir(file); dir != root; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir) //#nosec G703 -- callers validate local project paths; this loop only walks their parents up to root
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("project path traverses a non-directory or symlink: %s", dir)
		}
	}
	return nil
}

func replaceProjectFiles(root string, before, after map[string]projectFile) error {
	if err := writeProjectFiles(root, after); err != nil {
		return err
	}
	var removed []string
	for name := range before {
		if _, exists := after[name]; exists {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		removed = append(removed, filepath.Dir(path))
	}
	stdslices.Sort(removed)
	for _, dir := range stdslices.Backward(removed) {
		for dir != root {
			if err := os.Remove(dir); err != nil {
				break // Preserve directories containing untracked or retained files.
			}
			dir = filepath.Dir(dir)
		}
	}
	return nil
}
