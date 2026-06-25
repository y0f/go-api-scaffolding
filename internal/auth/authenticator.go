package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
)

// errUnauthorized is returned to the client for every authentication failure.
// The specific cause is logged server-side rather than leaked in the response.
var errUnauthorized = errors.New("unauthorized")

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
	req := input.RequestValidationInput.Request
	if input.SecuritySchemeName != "bearerAuth" {
		slog.WarnContext(req.Context(), "unexpected security scheme", slog.String("scheme", input.SecuritySchemeName))
		return errUnauthorized
	}
	token := bearerToken(req.Header.Get("Authorization"))
	if token == "" {
		return errUnauthorized
	}
	claims, err := a.verifier.Verify(req.Context(), token)
	if err != nil {
		slog.DebugContext(req.Context(), "bearer token verification failed", slog.Any("error", err))
		return errUnauthorized
	}
	h, ok := holderFrom(req.Context())
	if !ok {
		slog.ErrorContext(req.Context(), "principal holder not initialized; SeedContext must run before validation")
		return errUnauthorized
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
