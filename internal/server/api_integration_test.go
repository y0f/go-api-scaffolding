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
	"sync"
	"testing"
	"time"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	"github.com/y0f/go-api-scaffolding/internal/idempotency"
	"github.com/y0f/go-api-scaffolding/internal/modules/widget"
	"github.com/y0f/go-api-scaffolding/internal/observability"
	"github.com/y0f/go-api-scaffolding/internal/server"
	"github.com/y0f/go-api-scaffolding/internal/testutil"
)

func newTestServer(t *testing.T) (*httptest.Server, string) {
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

	handler := widget.NewHandler(
		widget.NewService(widget.NewRepository(pool), logger),
		idempotency.NewStore(pool, time.Hour),
	)
	router, err := server.NewRouter(server.RouterDeps{
		Logger:        logger,
		Telemetry:     telemetry,
		Health:        server.NewHealth(pool),
		Authenticator: auth.NewAuthenticator(verifier),
		WidgetHandler: handler,
		Config:        server.RouterConfig{RateLimitPerSecond: 1000, RateLimitBurst: 1000},
	})
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, token
}

func TestCreateRequiresAuth(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t)

	resp := do(t, srv, http.MethodPost, "/v1/widgets", "", "", `{"name":"x"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestCreateAndIdempotentReplay(t *testing.T) {
	t.Parallel()
	srv, token := newTestServer(t)

	first := do(t, srv, http.MethodPost, "/v1/widgets", token, "key-1", `{"name":"alpha"}`)
	defer first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(first.Body)
		t.Fatalf("create status = %d, want 201, body=%s", first.StatusCode, body)
	}

	second := do(t, srv, http.MethodPost, "/v1/widgets", token, "key-1", `{"name":"alpha"}`)
	defer second.Body.Close()
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("replay status = %d, want 201", second.StatusCode)
	}
	if second.Header.Get("Idempotency-Replayed") != "true" {
		t.Error("expected Idempotency-Replayed header on the replayed response")
	}
}

func TestValidationRejectsBadBody(t *testing.T) {
	t.Parallel()
	srv, token := newTestServer(t)

	resp := do(t, srv, http.MethodPost, "/v1/widgets", token, "", `{"name":""}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type = %q, want application/problem+json", ct)
	}
}

func TestConcurrentIdempotentCreateMakesOneWidget(t *testing.T) {
	t.Parallel()
	srv, token := newTestServer(t)

	const n = 6
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/widgets", strings.NewReader(`{"name":"race"}`))
			if err != nil {
				codes[i] = -1
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Idempotency-Key", "concurrent-key")
			resp, err := srv.Client().Do(req)
			if err != nil {
				codes[i] = -1
				return
			}
			codes[i] = resp.StatusCode
			resp.Body.Close()
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusCreated {
			t.Errorf("request %d code = %d, want 201", i, code)
		}
	}

	list := do(t, srv, http.MethodGet, "/v1/widgets", "", "", "")
	defer list.Body.Close()
	body, _ := io.ReadAll(list.Body)
	if !strings.Contains(string(body), `"total":1`) {
		t.Errorf("expected exactly one widget, got: %s", body)
	}
}

func do(t *testing.T, srv *httptest.Server, method, path, token, idemKey, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}
