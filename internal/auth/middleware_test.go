package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func devVerifier(t *testing.T) (Verifier, *DevIssuer) {
	t.Helper()
	v, issuer, err := NewVerifier(context.Background(), Settings{Issuer: "forge", Audience: "forge"}, true)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return v, issuer
}

func TestMiddlewareAllowsValidTokenAndPublishesPrincipal(t *testing.T) {
	v, issuer := devVerifier(t)
	token, err := issuer.Mint("user-1", []string{"admin"}, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	var got Principal
	var ok bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = PrincipalFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/things", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	Middleware(v)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !ok || got.Subject != "user-1" {
		t.Fatalf("principal = %+v ok=%v, want subject user-1", got, ok)
	}
}

func TestMiddlewareRejectsMissingToken(t *testing.T) {
	v, _ := devVerifier(t)
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/things", nil)
	Middleware(v)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Fatal("next handler must not run without a token")
	}
}

func TestMiddlewareRejectsInvalidToken(t *testing.T) {
	v, _ := devVerifier(t)
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/things", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	Middleware(v)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Fatal("next handler must not run with an invalid token")
	}
}
