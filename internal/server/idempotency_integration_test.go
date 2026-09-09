//go:build integration

package server_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func doIdempotent(t *testing.T, srv *httptest.Server, token, key, body string) *http.Response {
	t.Helper()
	return doWithHeaders(t, srv, http.MethodPost, "/v1/widgets", token, body, map[string]string{"Idempotency-Key": key})
}

func TestCreateAndIdempotentReplay(t *testing.T) {
	t.Parallel()
	srv, token, _ := newTestServer(t)

	first := doIdempotent(t, srv, token, "key-1", `{"name":"alpha"}`)
	defer first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(first.Body)
		t.Fatalf("create status = %d, want 201, body=%s", first.StatusCode, body)
	}

	second := doIdempotent(t, srv, token, "key-1", `{"name":"alpha"}`)
	defer second.Body.Close()
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("replay status = %d, want 201", second.StatusCode)
	}
	if second.Header.Get("Idempotency-Replayed") != "true" {
		t.Error("expected Idempotency-Replayed header on the replayed response")
	}
	if second.Header.Get("Location") != first.Header.Get("Location") {
		t.Errorf("replay Location = %q, want %q", second.Header.Get("Location"), first.Header.Get("Location"))
	}
}

func TestIdempotencyKeyConflictOnDifferentBody(t *testing.T) {
	t.Parallel()
	srv, token, _ := newTestServer(t)

	first := doIdempotent(t, srv, token, "conflict-key", `{"name":"alpha"}`)
	defer first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(first.Body)
		t.Fatalf("create status = %d, want 201, body=%s", first.StatusCode, body)
	}

	second := doIdempotent(t, srv, token, "conflict-key", `{"name":"beta"}`)
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("reuse status = %d, want 409", second.StatusCode)
	}
	if ct := second.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type = %q, want application/problem+json", ct)
	}
}

func TestConcurrentIdempotentCreateMakesOneWidget(t *testing.T) {
	t.Parallel()
	srv, token, _ := newTestServer(t)

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

	list := do(t, srv, http.MethodGet, "/v1/widgets", "", "")
	defer list.Body.Close()
	body, _ := io.ReadAll(list.Body)
	if !strings.Contains(string(body), `"total":1`) {
		t.Errorf("expected exactly one widget, got: %s", body)
	}
}

func TestIdempotencyKeyIsScopedToPrincipal(t *testing.T) {
	t.Parallel()
	srv, tokenA, issuer := newTestServer(t)
	tokenB, err := issuer.Mint("other", []string{"admin"}, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	first := doIdempotent(t, srv, tokenA, "shared-key", `{"name":"from-a"}`)
	defer first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", first.StatusCode)
	}
	firstBody, _ := io.ReadAll(first.Body)

	// Same key, different caller, different body. Before the key was scoped
	// this was a 409 (A's stored hash) or A's response replayed to B.
	second := doIdempotent(t, srv, tokenB, "shared-key", `{"name":"from-b"}`)
	defer second.Body.Close()
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("second create status = %d, want 201", second.StatusCode)
	}
	secondBody, _ := io.ReadAll(second.Body)
	if bytes.Equal(firstBody, secondBody) {
		t.Fatalf("second caller received the first caller's stored response: %s", secondBody)
	}
	if !strings.Contains(string(secondBody), `"from-b"`) {
		t.Fatalf("second response = %s, want the second caller's own widget", secondBody)
	}
}
