package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload. roles drives RBAC; scope is an optional OAuth2
// space-delimited scope string that is merged into the principal's scopes.
type Claims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles,omitempty"`
	Scope string   `json:"scope,omitempty"`
}

func (c *Claims) principal() Principal {
	var scopes []string
	if c.Scope != "" {
		scopes = strings.Fields(c.Scope)
	}
	return Principal{Subject: c.Subject, Roles: c.Roles, Scopes: scopes}
}

// Verifier validates a bearer token and returns its claims.
type Verifier interface {
	Verify(ctx context.Context, token string) (*Claims, error)
}

// Settings configures verifier selection without depending on the config
// package.
type Settings struct {
	JWKSURL       string
	PublicKeyPath string
	Issuer        string
	Audience      string
}

func newParser(s Settings) *jwt.Parser {
	opts := []jwt.ParserOption{jwt.WithValidMethods([]string{"RS256"})}
	if s.Issuer != "" {
		opts = append(opts, jwt.WithIssuer(s.Issuer))
	}
	if s.Audience != "" {
		opts = append(opts, jwt.WithAudience(s.Audience))
	}
	return jwt.NewParser(opts...)
}

type rsaVerifier struct {
	key    *rsa.PublicKey
	parser *jwt.Parser
}

func (v *rsaVerifier) Verify(_ context.Context, token string) (*Claims, error) {
	claims := &Claims{}
	if _, err := v.parser.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return v.key, nil
	}); err != nil {
		return nil, err
	}
	return claims, nil
}

type jwksVerifier struct {
	keyfunc keyfunc.Keyfunc
	parser  *jwt.Parser
}

func (v *jwksVerifier) Verify(_ context.Context, token string) (*Claims, error) {
	claims := &Claims{}
	if _, err := v.parser.ParseWithClaims(token, claims, v.keyfunc.Keyfunc); err != nil {
		return nil, err
	}
	return claims, nil
}

// DevIssuer mints tokens with the ephemeral key generated when no verifier is
// configured in development. It must never be enabled in production.
type DevIssuer struct {
	key      *rsa.PrivateKey
	issuer   string
	audience string
}

func (d *DevIssuer) Mint(subject string, roles []string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    d.issuer,
			Audience:  jwt.ClaimStrings{d.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Roles: roles,
	}
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(d.key)
}

// NewVerifier selects a verifier from settings. Precedence: JWKS (OIDC), then a
// static RSA public key, then an ephemeral development key. In any environment
// other than development, one of the first two must be configured.
func NewVerifier(ctx context.Context, s Settings, development bool) (Verifier, *DevIssuer, error) {
	parser := newParser(s)

	switch {
	case s.JWKSURL != "":
		kf, err := keyfunc.NewDefaultCtx(ctx, []string{s.JWKSURL})
		if err != nil {
			return nil, nil, fmt.Errorf("load jwks from %s: %w", s.JWKSURL, err)
		}
		return &jwksVerifier{keyfunc: kf, parser: parser}, nil, nil

	case s.PublicKeyPath != "":
		key, err := loadRSAPublicKey(s.PublicKeyPath)
		if err != nil {
			return nil, nil, err
		}
		return &rsaVerifier{key: key, parser: parser}, nil, nil

	case development:
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, nil, fmt.Errorf("generate development key: %w", err)
		}
		issuer := &DevIssuer{key: key, issuer: s.Issuer, audience: s.Audience}
		return &rsaVerifier{key: &key.PublicKey, parser: parser}, issuer, nil

	default:
		return nil, nil, errors.New("no token verifier configured")
	}
}

func loadRSAPublicKey(path string) (*rsa.PublicKey, error) {
	// path is operator-provided configuration, not user input.
	raw, err := os.ReadFile(path) //#nosec G304
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key in %s is not RSA", path)
	}
	return key, nil
}
