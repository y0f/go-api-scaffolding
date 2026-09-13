package server_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	spec "github.com/y0f/go-api-scaffolding/api"
	"github.com/y0f/go-api-scaffolding/internal/auth"
	"github.com/y0f/go-api-scaffolding/internal/observability"
	"github.com/y0f/go-api-scaffolding/internal/server"
)

// newRouter builds the public handler with no database and no API
// implementation, which is enough for the operational routes.
func newRouter(t *testing.T) http.Handler {
	t.Helper()
	telemetry, err := observability.Setup(context.Background(), observability.TelemetryConfig{ServiceName: "test"})
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	t.Cleanup(func() { _ = telemetry.Shutdown(context.Background()) })
	verifier, _, err := auth.NewVerifier(context.Background(), auth.Settings{}, true)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	router, err := server.NewRouter(server.RouterDeps{
		Logger:        slog.New(slog.DiscardHandler),
		Telemetry:     telemetry,
		Health:        server.NewHealth(nil),
		Authenticator: auth.NewAuthenticator(verifier),
		Config:        server.RouterConfig{RateLimitPerSecond: 1000, RateLimitBurst: 1000, MaxBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	return router
}

func TestSpecIsServedVerbatim(t *testing.T) {
	rec := httptest.NewRecorder()
	newRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("content-type = %q, want application/yaml", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), spec.OpenAPI) {
		t.Error("served spec differs from the embedded api/openapi.yaml")
	}
	if !bytes.HasPrefix(spec.OpenAPI, []byte("openapi: 3")) {
		t.Errorf("embedded spec does not start with an openapi version line: %q", spec.OpenAPI[:20])
	}
}

func TestDocsPagePointsAtSpec(t *testing.T) {
	rec := httptest.NewRecorder()
	newRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), `data-url="/openapi.yaml"`) {
		t.Error("docs page does not reference /openapi.yaml")
	}
}
