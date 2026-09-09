//go:build integration

package server_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	"github.com/y0f/go-api-scaffolding/internal/idempotency"
	"github.com/y0f/go-api-scaffolding/internal/modules/widget"
	"github.com/y0f/go-api-scaffolding/internal/observability"
	"github.com/y0f/go-api-scaffolding/internal/server"
	"github.com/y0f/go-api-scaffolding/internal/testutil"
)

func newTestServer(t *testing.T) (*httptest.Server, string, *auth.DevIssuer) {
	t.Helper()
	pool := testutil.NewDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	telemetry, err := observability.Setup(context.Background(), observability.TelemetryConfig{ServiceName: "test"})
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	verifier, issuer, err := auth.NewVerifier(context.Background(), auth.Settings{Issuer: "forge", Audience: "forge"}, true)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	token, err := issuer.Mint("tester", []string{"admin"}, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	handler := widget.NewHandler(widget.NewService(widget.NewRepository(pool)))
	//forge:begin idempotency
	handler.EnableIdempotency(idempotency.NewStore(pool, time.Hour))
	//forge:end idempotency
	router, err := server.NewRouter(server.RouterDeps{
		Logger:        logger,
		Telemetry:     telemetry,
		Health:        server.NewHealth(pool),
		Authenticator: auth.NewAuthenticator(verifier),
		API:           handler,
		Config:        server.RouterConfig{RateLimitPerSecond: 1000, RateLimitBurst: 1000, MaxBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, token, issuer
}

func TestCreateRequiresAuth(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	resp := do(t, srv, http.MethodPost, "/v1/widgets", "", `{"name":"x"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestValidationRejectsBadBody(t *testing.T) {
	t.Parallel()
	srv, token, _ := newTestServer(t)

	resp := do(t, srv, http.MethodPost, "/v1/widgets", token, `{"name":""}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type = %q, want application/problem+json", ct)
	}
}

func TestRejectsOversizeBody(t *testing.T) {
	t.Parallel()
	srv, token, _ := newTestServer(t)

	oversize := `{"name":"` + strings.Repeat("x", 2<<20) + `"}`
	resp := do(t, srv, http.MethodPost, "/v1/widgets", token, oversize)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func do(t *testing.T, srv *httptest.Server, method, path, token, body string) *http.Response {
	return doWithHeaders(t, srv, method, path, token, body, nil)
}

func doWithHeaders(t *testing.T, srv *httptest.Server, method, path, token, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func TestUnknownRouteAndMethodAreProblems(t *testing.T) {
	t.Parallel()
	srv, token, _ := newTestServer(t)

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/v1/nope", http.StatusNotFound},
		{http.MethodPatch, "/v1/widgets", http.StatusMethodNotAllowed},
	} {
		resp := do(t, srv, tc.method, tc.path, token, "")
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s status = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("%s %s content-type = %q, want application/problem+json", tc.method, tc.path, ct)
		}
		resp.Body.Close()
	}
}
