package auth

import (
	"context"
	"testing"
	"time"
)

func TestDevIssuerRoundTrip(t *testing.T) {
	verifier, issuer, err := NewVerifier(context.Background(), Settings{Issuer: "forge", Audience: "forge"}, true)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	if issuer == nil {
		t.Fatal("expected a development issuer")
	}

	token, err := issuer.Mint("user-1", []string{"admin"}, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	claims, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "user-1" {
		t.Errorf("subject = %q, want user-1", claims.Subject)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "admin" {
		t.Errorf("roles = %v, want [admin]", claims.Roles)
	}
}

func TestVerifierRequiredOutsideDevelopment(t *testing.T) {
	if _, _, err := NewVerifier(context.Background(), Settings{}, false); err == nil {
		t.Fatal("expected an error when no verifier is configured")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	verifier, issuer, err := NewVerifier(context.Background(), Settings{Issuer: "forge", Audience: "forge"}, true)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	token, err := issuer.Mint("user-1", nil, -time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := verifier.Verify(context.Background(), token); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}
