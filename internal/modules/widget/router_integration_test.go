//go:build integration

package widget_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	//forge:begin idempotency
	"github.com/y0f/go-api-scaffolding/internal/idempotency"
	//forge:end idempotency
	"github.com/y0f/go-api-scaffolding/internal/modules/widget"
	"github.com/y0f/go-api-scaffolding/internal/observability"
	"github.com/y0f/go-api-scaffolding/internal/server"
	"github.com/y0f/go-api-scaffolding/internal/testutil"
)

// newRouterServer serves the widget API through the real router, so requests
// pass the OpenAPI validator and the authenticator as they do in production.
func newRouterServer(t *testing.T) (*httptest.Server, *auth.DevIssuer, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.NewDB(t)
	tel, err := observability.Setup(context.Background(), observability.TelemetryConfig{ServiceName: "widget-test"})
	if err != nil {
		t.Fatal(err)
	}
	verifier, issuer, err := auth.NewVerifier(context.Background(), auth.Settings{Issuer: "forge", Audience: "forge"}, true)
	if err != nil {
		t.Fatal(err)
	}
	h := widget.NewHandler(widget.NewService(widget.NewRepository(pool)))
	//forge:begin idempotency
	h.EnableIdempotency(idempotency.NewStore(pool, time.Hour))
	//forge:end idempotency
	router, err := server.NewRouter(server.RouterDeps{
		Logger: slog.New(slog.DiscardHandler), Telemetry: tel, Health: server.NewHealth(pool),
		Authenticator: auth.NewAuthenticator(verifier), API: h,
		Config: server.RouterConfig{RateLimitPerSecond: 1000, RateLimitBurst: 1000, MaxBodyBytes: 1 << 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, issuer, pool
}

func send(t *testing.T, srv *httptest.Server, method, path, token, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

func mint(t *testing.T, issuer *auth.DevIssuer, subject string) string {
	t.Helper()
	token, err := issuer.Mint(subject, []string{"admin"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

//forge:begin idempotency

// Idempotency keys are scoped to the token's subject. A token without one must
// not share a namespace with every other such token, where one caller would be
// served another's stored create.
func TestIdempotencyKeyRequiresASubject(t *testing.T) {
	t.Parallel()
	srv, issuer, pool := newRouterServer(t)
	token := mint(t, issuer, "")

	resp, body := send(t, srv, http.MethodPost, "/v1/widgets", token, `{"name":"x"}`, map[string]string{"Idempotency-Key": "1"})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "subject (sub) claim") {
		t.Fatalf("subject-less idempotent create = %d %s, want 400 asking for a subject (sub) claim", resp.StatusCode, body)
	}
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM widgets").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d widgets created, want 0", n)
	}
}

//forge:end idempotency

// A body must be exactly one JSON document: a second value or trailing bytes
// mean a truncated or concatenated payload, not a valid request.
func TestRejectsTrailingDataAfterTheJSONBody(t *testing.T) {
	t.Parallel()
	srv, issuer, _ := newRouterServer(t)
	token := mint(t, issuer, "u")

	resp, body := send(t, srv, http.MethodPost, "/v1/widgets", token, `{"name":"a"}`, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("setup create = %d %s", resp.StatusCode, body)
	}
	var created struct{ ID string }
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ name, method, path, body string }{
		{"replace with every field and trailing garbage", http.MethodPut, "/v1/widgets/" + created.ID, `{"name":"a","description":"","status":"active"}xyz`},
		{"replace with trailing garbage", http.MethodPut, "/v1/widgets/" + created.ID, `{"name":"a"}xyz`},
		{"create with two objects", http.MethodPost, "/v1/widgets", `{"name":"a"} {"name":"b"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := send(t, srv, tc.method, tc.path, token, tc.body, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s %s = %d %s, want 400", tc.method, tc.path, resp.StatusCode, body)
			}
		})
	}
}

// Every skipped row is still read, so the offset is capped by the contract.
func TestListRejectsAnOffsetBeyondTheMaximum(t *testing.T) {
	t.Parallel()
	srv, issuer, _ := newRouterServer(t)
	token := mint(t, issuer, "u")

	if resp, body := send(t, srv, http.MethodGet, "/v1/widgets?offset=10000", token, "", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("offset=10000 = %d %s, want 200", resp.StatusCode, body)
	}
	if resp, body := send(t, srv, http.MethodGet, "/v1/widgets?offset=2147483647", token, "", nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("offset=2147483647 = %d %s, want 400", resp.StatusCode, body)
	}
}

// The list order (created_at DESC, id DESC) must be served by an index, so a
// page does not sort the whole table.
func TestListPageIsReadFromTheIndex(t *testing.T) {
	t.Parallel()
	pool := testutil.NewDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO widgets (name) SELECT 'w' || g FROM generate_series(1, 20000) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "ANALYZE widgets"); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, "EXPLAIN SELECT * FROM widgets ORDER BY created_at DESC, id DESC LIMIT 20 OFFSET 0")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "widgets_created_at_idx") || strings.Contains(joined, "Sort") {
		t.Errorf("list page is not an ordered index scan:\n%s", joined)
	}
}
