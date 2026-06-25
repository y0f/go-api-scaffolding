package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
)

// Authenticator verifies bearer tokens during OpenAPI request validation and
// publishes the resulting principal to the request context.
type Authenticator struct {
	verifier Verifier
}

func NewAuthenticator(v Verifier) *Authenticator {
	return &Authenticator{verifier: v}
}

// SeedContext prepares the request context to receive the authenticated
// principal. It must run before the OpenAPI request-validation middleware,
// which does not propagate a context modified inside Authenticate.
func (a *Authenticator) SeedContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(withHolder(r.Context())))
	})
}

// Authenticate satisfies openapi3filter.AuthenticationFunc. The validator calls
// it for operations that declare the bearerAuth scheme.
func (a *Authenticator) Authenticate(_ context.Context, input *openapi3filter.AuthenticationInput) error {
	if input.SecuritySchemeName != "bearerAuth" {
		return fmt.Errorf("unsupported security scheme %q", input.SecuritySchemeName)
	}
	req := input.RequestValidationInput.Request
	token := bearerToken(req.Header.Get("Authorization"))
	if token == "" {
		return errors.New("missing bearer token")
	}
	claims, err := a.verifier.Verify(req.Context(), token)
	if err != nil {
		return fmt.Errorf("verify token: %w", err)
	}
	h, ok := holderFrom(req.Context())
	if !ok {
		return errors.New("principal holder not initialized; SeedContext middleware must run before validation")
	}
	principal := claims.principal()
	h.principal = &principal
	return nil
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}
