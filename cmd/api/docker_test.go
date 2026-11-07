//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestDockerAPI uses its own Compose project, ports and volume. It exercises
// the shipped image and migrations through HTTP, then verifies persistence.
func TestDockerAPI(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	project := fmt.Sprintf("forge-e2e-%d", time.Now().UnixNano())
	// Let Docker assign both ports. A listen/close reservation can be taken by
	// another parallel test before Compose binds it. Ambient observability
	// settings must not start sidecars with shared ports or remote exporters.
	env := append(os.Environ(), "FORGE_DEV_API_PORT=0", "FORGE_DEV_DB_PORT=0", "COMPOSE_PROFILES=", "FORGE_OTEL_OTLP_ENDPOINT=")
	docker := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Dir, cmd.Env = root, env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	compose := func(args ...string) (string, error) {
		return docker(append([]string{"compose", "-p", project, "-f", "deployments/docker-compose.yml"}, args...)...)
	}
	t.Cleanup(func() {
		if out, err := compose("down", "--volumes", "--remove-orphans"); err != nil {
			t.Errorf("clean isolated Compose project: %v\n%s", err, out)
		}
	})
	if out, err := compose("up", "--build", "-d"); err != nil {
		t.Fatalf("start Compose: %v\n%s", err, out)
	}
	var base string
	refreshAddress := func() {
		t.Helper()
		out, err := compose("port", "api", "8080")
		if err != nil {
			t.Fatalf("discover isolated API port: %v\n%s", err, out)
		}
		host, _, err := net.SplitHostPort(strings.TrimSpace(out))
		if err != nil || host != "127.0.0.1" {
			t.Fatalf("API must publish on loopback: %q, %v", out, err)
		}
		base = "http://" + strings.TrimSpace(out)
	}
	refreshAddress()
	client := &http.Client{Timeout: 5 * time.Second}
	waitReady := func() {
		t.Helper()
		deadline := time.Now().Add(time.Minute)
		for time.Now().Before(deadline) {
			response, err := client.Get(base + "/readyz")
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("API did not become ready")
	}
	waitReady()
	logs, err := compose("logs", "--no-color", "api")
	if err != nil {
		t.Fatalf("read development auth log: %v", err)
	}
	tokens := regexp.MustCompile(`bearer token: ([A-Za-z0-9_.-]+)`).FindAllStringSubmatch(logs, -1)
	if len(tokens) == 0 {
		t.Fatal("development token missing from API log")
	}
	token := tokens[len(tokens)-1][1]
	send := func(method, path, auth, body, key string, want int) ([]byte, http.Header) {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, response.StatusCode, want, data)
		}
		return data, response.Header
	}
	send(http.MethodGet, "/livez", "", "", "", http.StatusOK)
	send(http.MethodGet, "/docs", "", "", "", http.StatusOK)
	spec, _ := send(http.MethodGet, "/openapi.yaml", "", "", "", http.StatusOK)
	if !strings.Contains(string(spec), "/v1/widgets:") {
		t.Fatal("served contract does not contain widgets")
	}
	send(http.MethodPost, "/v1/widgets", "", `{"name":"demo"}`, "", http.StatusUnauthorized)
	send(http.MethodPost, "/v1/widgets", token, `{}`, "", http.StatusBadRequest)
	send(http.MethodPost, "/v1/widgets", token, `{"name":"demo"} {}`, "", http.StatusBadRequest)
	send(http.MethodGet, "/v1/widgets?offset=2147483648", "", "", "", http.StatusBadRequest)
	first, headers := send(http.MethodPost, "/v1/widgets", token, `{"name":"demo"}`, "docker-retry", http.StatusCreated)
	location := headers.Get("Location")
	if !strings.HasPrefix(location, "/v1/widgets/") {
		t.Fatalf("invalid Location: %q", location)
	}
	//forge:begin idempotency
	replay, replayHeaders := send(http.MethodPost, "/v1/widgets", token, `{"name":"demo"}`, "docker-retry", http.StatusCreated)
	if string(first) != string(replay) || replayHeaders.Get("Idempotency-Replayed") != "true" || replayHeaders.Get("Location") != location {
		t.Fatal("idempotent retry changed the response")
	}
	send(http.MethodPost, "/v1/widgets", token, `{"name":"different"}`, "docker-retry", http.StatusConflict)
	//forge:end idempotency
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(first, &created); err != nil || created.ID == "" {
		t.Fatal("created response has no ID")
	}
	send(http.MethodPut, location, token, `{"name":"updated"}`, "", http.StatusOK)
	if out, err := compose("stop", "api"); err != nil {
		t.Fatalf("stop API gracefully: %v\n%s", err, out)
	}
	containerID, err := compose("ps", "--all", "--quiet", "api")
	if err != nil || strings.TrimSpace(containerID) == "" {
		t.Fatalf("find stopped API: %v\n%s", err, containerID)
	}
	exitCode, err := docker("inspect", "--format", "{{.State.ExitCode}}", strings.TrimSpace(containerID))
	if err != nil || strings.TrimSpace(exitCode) != "0" {
		t.Fatalf("API did not finish shutdown before Docker's grace deadline: exit=%q, error=%v", exitCode, err)
	}
	logs, err = compose("logs", "--no-color", "api")
	if err != nil || !strings.Contains(logs, "http server stopped cleanly") {
		clean := regexp.MustCompile(`bearer token: [A-Za-z0-9_.-]+`).ReplaceAllString(logs, "bearer token: [redacted]")
		t.Fatalf("stop did not complete graceful shutdown: %v\n%s", err, clean)
	}
	// Recreate both containers while keeping the named volume. This catches a
	// broken volume mount that an API-only restart would leave undetected.
	if out, err := compose("down"); err != nil {
		t.Fatalf("stop stack preserving data: %v\n%s", err, out)
	}
	if out, err := compose("up", "-d"); err != nil {
		t.Fatalf("recreate stack: %v\n%s", err, out)
	}
	refreshAddress()
	waitReady()
	persisted, _ := send(http.MethodGet, location, "", "", "", http.StatusOK)
	if !strings.Contains(string(persisted), `"name":"updated"`) {
		t.Fatal("data was not retained after recreating the API and database containers")
	}
	logs, err = compose("logs", "--no-color", "api")
	if err != nil {
		t.Fatalf("read restarted API log: %v", err)
	}
	tokens = regexp.MustCompile(`bearer token: ([A-Za-z0-9_.-]+)`).FindAllStringSubmatch(logs, -1)
	if len(tokens) == 0 {
		t.Fatal("restarted API did not log a development token")
	}
	token = tokens[len(tokens)-1][1]
	send(http.MethodDelete, location, token, "", "", http.StatusNoContent)
	send(http.MethodGet, location, "", "", "", http.StatusNotFound)
}
