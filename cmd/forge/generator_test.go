//go:build generator

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestGeneratedProjects exercises the user journey in isolated projects. Run
// with -tags generator; Docker and the pinned toolchain downloads are required.
func TestGeneratedProjects(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	for _, preset := range []struct{ name, drop string }{
		{"full", ""}, {"minimal", "example,outbox,idempotency,otlp,admin"},
	} {
		t.Run(preset.name, func(t *testing.T) {
			dir := t.TempDir()
			files := strings.Split(strings.TrimSpace(generatorCommand(t, root, "git", "ls-files", "--cached", "--others", "--exclude-standard")), "\n")
			for _, name := range files {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				src, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				dst := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dst, src, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			generatorCommand(t, dir, "git", "init", "--quiet")
			generatorCommand(t, dir, "git", "add", "--all")
			generatorCommand(t, dir, "git", "-c", "user.name=Generator Fixture", "-c", "user.email=generator-fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "test: initialize generator fixture")
			module := "example.com/generator/" + preset.name
			generatorCommand(t, dir, "go", "run", "./cmd/forge", "init", "-yes", "-module", module, "-drop", preset.drop)
			for _, resource := range []string{"Order", "Category", "Api", "Http", "Idempotency", "Store", "AuditTest", "InvoiceLinux"} {
				generatorCommand(t, dir, "go", "run", "./cmd/forge", "add", "resource", resource)
			}
			generatorCommand(t, dir, "go", "run", "./cmd/dev", "generate")
			grader, err := render("journey_test.go.tmpl", resource{Module: module, Idempotency: preset.name == "full"})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "cmd/api/generated_journey_test.go"), grader, 0o600); err != nil {
				t.Fatal(err)
			}
			generatorCommand(t, dir, "go", "run", "./cmd/dev", "check")
			generatorCommand(t, dir, "go", "test", "-tags", "integration", "-count=1", "./cmd/api")
		})
	}
}

func generatorCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}
