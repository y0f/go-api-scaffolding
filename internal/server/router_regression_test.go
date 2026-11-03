package server_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	"github.com/y0f/go-api-scaffolding/internal/observability"
	"github.com/y0f/go-api-scaffolding/internal/server"
)

func regressionRouter(t *testing.T, maxBody int64) (http.Handler, string) {
	t.Helper()
	telemetry, err := observability.Setup(context.Background(), observability.TelemetryConfig{ServiceName: "test"})
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	t.Cleanup(func() { _ = telemetry.Shutdown(context.Background()) })
	verifier, issuer, err := auth.NewVerifier(context.Background(), auth.Settings{Issuer: "forge", Audience: "forge"}, true)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	token, err := issuer.Mint("tester", []string{"admin"}, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	router, err := server.NewRouter(server.RouterDeps{
		Logger:        slog.New(slog.DiscardHandler),
		Telemetry:     telemetry,
		Health:        server.NewHealth(nil),
		Authenticator: auth.NewAuthenticator(verifier),
		Config: server.RouterConfig{
			RateLimitPerSecond: 1000, RateLimitBurst: 1000, MaxBodyBytes: maxBody,
			CORSAllowedOrigins: []string{"https://app.example"},
		},
	})
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	return router, token
}

// The Host header is client-controlled, so it must not become a metric label.
func TestHostHeaderDoesNotCreateMetricSeries(t *testing.T) {
	router, _ := regressionRouter(t, 1<<20)
	for i := range 5 {
		req := httptest.NewRequest(http.MethodGet, "/livez", nil)
		req.Host = fmt.Sprintf("evil%d.example:%d", i, 1000+i)
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	n := 0
	for i := range 5 {
		if strings.Contains(body, fmt.Sprintf(`server_address="evil%d.example"`, i)) {
			n++
		}
	}
	if n > 0 {
		t.Fatalf("attacker Host values became metric labels: %d of 5 distinct server_address series", n)
	}
}

// An oversized chunked body on a secured operation is a 413, not a 401 from
// the security check that buffers the body.
func TestChunkedOversizeBodyIs413(t *testing.T) {
	router, token := regressionRouter(t, 64)
	body := `{"name":"` + strings.Repeat("a", 200) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/widgets", strings.NewReader(body))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", rec.Code, rec.Body.String())
	}
}

// Every 401 carries a Bearer challenge (RFC 9110 15.5.2, RFC 6750 section 3).
func TestUnauthorizedHasWWWAuthenticate(t *testing.T) {
	router, _ := regressionRouter(t, 1<<20)
	req := httptest.NewRequest(http.MethodDelete, "/v1/widgets/00000000-0000-0000-0000-000000000001", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (precondition)", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatal("401 response has no WWW-Authenticate header")
	}
}

// Rotating the interface ID within one IPv6 /64 must not yield fresh buckets.
func TestRateLimitGroupsIPv6By64(t *testing.T) {
	handler := server.RateLimit(0.001, 1)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	allowed := 0
	for i := 1; i <= 50; i++ {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = fmt.Sprintf("[2001:db8::%x]:443", i)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			allowed++
		}
	}
	if allowed > 1 {
		t.Fatalf("burst=1 limiter allowed %d requests from one /64 by rotating the interface ID", allowed)
	}
}

// Location and Idempotency-Replayed are readable by allowed cross-origin callers.
func TestCORSExposesResponseHeaders(t *testing.T) {
	handler := server.CORS([]string{"https://app.example"}, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/v1/widgets/1")
		w.Header().Set("Idempotency-Replayed", "true")
		w.WriteHeader(http.StatusCreated)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/widgets", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatal("precondition: origin not allowed")
	}
	exp := rec.Header().Get("Access-Control-Expose-Headers")
	if !strings.Contains(exp, "Location") || !strings.Contains(exp, "Idempotency-Replayed") {
		t.Fatalf("Access-Control-Expose-Headers = %q; browser cannot read Location/Idempotency-Replayed", exp)
	}
}

// A panic after the response started aborts the connection instead of
// appending a problem body to the partial response.
func TestRecovererAbortsStartedResponse(t *testing.T) {
	handler := server.Recoverer(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{"))
		panic("boom")
	}))
	srv := httptest.NewServer(handler)
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		return // connection aborted: the desired behaviour
	}
	defer resp.Body.Close()
	b, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return // aborted mid-body: desired
	}
	t.Fatalf("panicking handler produced a complete response: status=%d body=%q", resp.StatusCode, string(b))
}

// Validator problem details stay one line, without the schema/value dump.
func TestValidatorDetailHasNoSchemaDump(t *testing.T) {
	router, _ := regressionRouter(t, 1<<20)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/widgets?limit=0", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (precondition); body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Schema:") {
		t.Fatalf("problem detail contains an embedded schema dump: %s", rec.Body.String())
	}
}
