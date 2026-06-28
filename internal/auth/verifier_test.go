package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testRSAVerifier builds an rsaVerifier over a fresh key with issuer and
// audience pinned to "forge", plus the private key for minting test tokens.
func testRSAVerifier(t *testing.T) (*rsa.PrivateKey, *rsaVerifier) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key, &rsaVerifier{key: &key.PublicKey, parser: newParser(Settings{Issuer: "forge", Audience: "forge"})}
}

func signRS256(t *testing.T, key *rsa.PrivateKey, claims Claims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

func validClaims() Claims {
	now := time.Now()
	return Claims{RegisteredClaims: jwt.RegisteredClaims{
		Subject:   "user-1",
		Issuer:    "forge",
		Audience:  jwt.ClaimStrings{"forge"},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}}
}

func TestVerifyRejectsWrongIssuer(t *testing.T) {
	key, v := testRSAVerifier(t)
	claims := validClaims()
	claims.Issuer = "evil"
	if _, err := v.Verify(context.Background(), signRS256(t, key, claims)); err == nil {
		t.Fatal("expected a token with the wrong issuer to be rejected")
	}
}

func TestVerifyRejectsWrongAudience(t *testing.T) {
	key, v := testRSAVerifier(t)
	claims := validClaims()
	claims.Audience = jwt.ClaimStrings{"evil"}
	if _, err := v.Verify(context.Background(), signRS256(t, key, claims)); err == nil {
		t.Fatal("expected a token with the wrong audience to be rejected")
	}
}

func TestVerifyRejectsMissingExpiry(t *testing.T) {
	key, v := testRSAVerifier(t)
	claims := validClaims()
	claims.ExpiresAt = nil
	if _, err := v.Verify(context.Background(), signRS256(t, key, claims)); err == nil {
		t.Fatal("expected a token without an exp claim to be rejected")
	}
}

func TestVerifyRejectsAlgorithmConfusion(t *testing.T) {
	key, v := testRSAVerifier(t)
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	// Classic RS256->HS256 confusion: sign with HS256 using the RSA public key
	// bytes as the HMAC secret. RS256 pinning must reject the algorithm before
	// the key is ever consulted.
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims()).SignedString(pubDER)
	if err != nil {
		t.Fatalf("sign hs256: %v", err)
	}
	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("expected an HS256 token to be rejected by RS256 pinning")
	}
}

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
