package auth

import (
	"log/slog"
	"net/http"

	"github.com/y0f/go-api-scaffolding/internal/platform/problem"
)

// Middleware authenticates a request from its bearer token and publishes the
// resulting principal to the context for downstream PrincipalFrom calls. Unlike
// the OpenAPI-driven Authenticator, it is a plain net/http middleware for routes
// mounted outside the spec-first flow, such as the modules stamped by cmd/forge.
// Requests without a valid token are rejected with 401 before the handler runs,
// so wrapping a route with it is secure by default. The specific failure cause
// is logged server-side rather than leaked to the client.
func Middleware(v Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r.Header.Get("Authorization"))
			if token == "" {
				problem.Status(w, r, http.StatusUnauthorized, "unauthorized")
				return
			}
			claims, err := v.Verify(r.Context(), token)
			if err != nil {
				slog.DebugContext(r.Context(), "bearer token verification failed", slog.Any("error", err))
				problem.Status(w, r, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), claims.principal())))
		})
	}
}
