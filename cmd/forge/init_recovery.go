package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	stdslices "slices"
	"strings"
)

const recoveryDirectory = ".forge-init-recovery"

type recoveryManifest struct {
	Version    int      `json:"version"`
	Publishing bool     `json:"publishing"`
	Before     []string `json:"before"`
	After      []string `json:"after"`
}

// activateProjectRecovery is the last step before original files are touched.
// A process interrupted before this point only needs its unused backup removed.
func activateProjectRecovery(root string) error {
	directory, manifest, err := readProjectRecovery(root)
	if err != nil {
		return err
	}
	manifest.Publishing = true
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	next := filepath.Join(directory, "manifest.next")
	if err := persistRecoveryFile(next, append(content, '\n'), 0o600); err != nil {
		return err
	}
	backup, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = backup.Close() }()
	return backup.Rename("manifest.next", "manifest.json")
}

// saveProjectRecovery publishes the backup before any original file changes.
// Interrupted backup preparation can leave a .forge-init-backup-* directory,
// but cannot change the project. Only a complete backup receives the fixed name.
func saveProjectRecovery(root string, before, after map[string]projectFile) (result error) {
	destination := filepath.Join(root, recoveryDirectory)
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) { //#nosec G703 -- fixed journal name in the current project root
		return errors.New("init recovery backup already exists; run forge init -recover")
	}
	stage, err := os.MkdirTemp(root, ".forge-init-backup-*")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(stage)) }() //#nosec G703 -- stage is the private directory just returned by MkdirTemp
	manifest := recoveryManifest{Version: 1}
	for name, file := range before {
		if err := validateRecoveryPath(name); err != nil {
			return err
		}
		if err := persistRecoveryFile(filepath.Join(stage, "files", filepath.FromSlash(name)), file.Data, file.Mode); err != nil {
			return err
		}
		manifest.Before = append(manifest.Before, name)
	}
	for name := range after {
		if err := validateRecoveryPath(name); err != nil {
			return err
		}
		manifest.After = append(manifest.After, name)
	}
	stdslices.Sort(manifest.Before)
	stdslices.Sort(manifest.After)
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := persistRecoveryFile(filepath.Join(stage, "manifest.json"), append(content, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(stage, destination) //#nosec G703 -- both paths are private backup locations created within root
}

func persistRecoveryFile(path string, data []byte, mode os.FileMode) (result error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil { //#nosec G703 -- callers pass validated local names inside the private backup directory
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //#nosec G304 G703 -- private backup directory and validated local paths
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

// recoverProject deliberately continues after individual failures, leaving its
// backup intact unless every restoration succeeds. The Git index is untouched.
func recoverProject(root string) error {
	directory, manifest, err := readProjectRecovery(root)
	if err != nil {
		return err
	}
	if !manifest.Publishing {
		return discardProjectRecovery(root)
	}
	before, err := readProjectFiles(filepath.Join(directory, "files"), manifest.Before)
	if err != nil {
		return fmt.Errorf("read init recovery originals: %w", err)
	}
	var failures []error
	// A sorted order makes recovery reports and injected-failure tests stable.
	for _, name := range manifest.Before {
		if err := restoreProjectFile(root, name, before[name]); err != nil {
			failures = append(failures, fmt.Errorf("restore %s: %w", name, err))
		}
	}
	for _, name := range manifest.After {
		if _, existed := before[name]; existed {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := checkProjectParents(root, path); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, fmt.Errorf("remove initialized file %s: %w", name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("recovery incomplete; backup retained at %s; correct the errors and rerun forge init -recover: %w", directory, errors.Join(failures...))
	}
	if err := discardProjectRecovery(root); err != nil {
		return fmt.Errorf("original files restored but recovery backup could not be removed: %w", err)
	}
	return nil
}

func readProjectRecovery(root string) (string, recoveryManifest, error) {
	var manifest recoveryManifest
	directory := filepath.Join(root, recoveryDirectory)
	project, err := os.OpenRoot(root)
	if err != nil {
		return "", manifest, err
	}
	defer func() { _ = project.Close() }()
	info, err := project.Lstat(recoveryDirectory)
	if err != nil {
		return "", manifest, fmt.Errorf("read init recovery backup: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", manifest, errors.New("init recovery backup must be a real directory")
	}
	backup, err := project.OpenRoot(recoveryDirectory)
	if err != nil {
		return "", manifest, err
	}
	defer func() { _ = backup.Close() }()
	info, err = backup.Lstat("manifest.json")
	if err != nil {
		return "", manifest, err
	}
	if !info.Mode().IsRegular() {
		return "", manifest, errors.New("init recovery manifest must be a regular file")
	}
	content, err := backup.ReadFile("manifest.json")
	if err != nil {
		return "", manifest, err
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		return "", manifest, fmt.Errorf("read init recovery manifest: %w", err)
	}
	if manifest.Version != 1 {
		return "", manifest, fmt.Errorf("unsupported init recovery format %d; use the forge release that started init", manifest.Version)
	}
	if manifest.Publishing && len(manifest.Before) > 0 {
		info, err = backup.Lstat("files")
		if err != nil {
			return "", manifest, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", manifest, errors.New("init recovery originals must be a real directory")
		}
	}
	for _, name := range append(stdslices.Clone(manifest.Before), manifest.After...) {
		if err := validateRecoveryPath(name); err != nil {
			return "", manifest, err
		}
	}
	return directory, manifest, nil
}

func restoreProjectFile(root, name string, file projectFile) error {
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := checkProjectParents(root, path); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm() == file.Mode {
		content, readErr := os.ReadFile(path) //#nosec G304 -- validated local path from recovery manifest
		if readErr == nil && bytes.Equal(content, file.Data) {
			return nil // Also permits retrying a partially completed read-only restore.
		}
	}
	if err := writeProjectFiles(root, map[string]projectFile{name: file}); err != nil {
		return err
	}
	return os.Chmod(path, file.Mode)
}

// Retire the fixed journal name before deleting backup files. If the process
// exits during cleanup, the fully published/restored project remains usable and
// the leftover retired directory cannot be mistaken for an unfinished init.
func discardProjectRecovery(root string) (result error) {
	retired, err := os.MkdirTemp(root, ".forge-init-retired-*")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(retired)) }()
	return os.Rename(filepath.Join(root, recoveryDirectory), filepath.Join(retired, "backup"))
}

func validateRecoveryPath(name string) error {
	if !filepath.IsLocal(name) || filepath.Clean(name) == "." {
		return fmt.Errorf("invalid recovery path: %s", name)
	}
	first, _, _ := strings.Cut(filepath.ToSlash(filepath.Clean(name)), "/")
	if strings.EqualFold(first, ".git") || strings.EqualFold(first, recoveryDirectory) || strings.HasPrefix(strings.ToLower(first), ".forge-init-backup-") {
		return fmt.Errorf("reserved recovery path: %s", name)
	}
	return nil
}
