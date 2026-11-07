package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	spec "github.com/y0f/gophling/api"
	"github.com/y0f/gophling/internal/auth"
	api "github.com/y0f/gophling/internal/gen/api"
	"github.com/y0f/gophling/internal/observability"
	"github.com/y0f/gophling/internal/platform/problem"
)

type RouterConfig struct {
	TrustedProxies     []netip.Prefix
	CORSAllowedOrigins []string
	RateLimitPerSecond float64
	RateLimitBurst     int
	MaxBodyBytes       int64
	Development        bool
}

type RouterDeps struct {
	Logger        *slog.Logger
	Telemetry     *observability.Telemetry
	Health        *Health
	Authenticator *auth.Authenticator
	// API implements the operations in api/openapi.yaml. Nil serves every
	// operation as 501, which is the state of a project before its first
	// resource.
	API api.ServerInterface
	// Mounts is an escape hatch for routes outside the OpenAPI contract.
	// These routes run global middleware but are not spec-validated.
	// Generated resources use API instead.
	Mounts []func(chi.Router)
	Config RouterConfig
}

// NewRouter assembles the public HTTP handler: global middleware, unauthenticated
// operational endpoints, and the spec-validated, authenticated API routes.
func NewRouter(deps RouterDeps) (http.Handler, error) {
	swagger, err := api.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load openapi spec: %w", err)
	}
	// The spec is matched by path only; clearing servers avoids host mismatches.
	swagger.Servers = nil

	r := chi.NewRouter()
	// First, so the access log and the rate limiter see the client, not the proxy.
	r.Use(RealIP(deps.Config.TrustedProxies))
	r.Use(middleware.RequestID)
	// A fixed server name keeps the client-controlled Host header out of the
	// server.address/server.port metric labels, which would otherwise mint a new
	// series per Host value. Port 0 is never recorded, so it stays unlabelled.
	r.Use(otelhttp.NewMiddleware("forge", otelhttp.WithServerName("forge:0")))
	r.Use(SecureHeaders)
	r.Use(AccessLog(deps.Logger))
	r.Use(Recoverer(deps.Logger))
	r.Use(CORS(deps.Config.CORSAllowedOrigins, deps.Config.Development))
	r.Use(RateLimit(deps.Config.RateLimitPerSecond, deps.Config.RateLimitBurst))
	r.Use(MaxBytes(deps.Config.MaxBodyBytes))
	// Seed the principal holder before the validator runs authentication.
	r.Use(deps.Authenticator.SeedContext)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		problem.Status(w, r, http.StatusNotFound, "no such route")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		problem.Status(w, r, http.StatusMethodNotAllowed, "method not allowed")
	})

	r.Get("/livez", deps.Health.Livez)
	r.Get("/readyz", deps.Health.Readyz)
	r.Handle("/metrics", promhttp.HandlerFor(deps.Telemetry.Registry, promhttp.HandlerOpts{}))
	r.Get("/openapi.yaml", SpecHandler(spec.OpenAPI))
	r.Get("/docs", DocsHandler)

	// The validator enforces the contract (params, bodies, enums) and runs the
	// security scheme's authentication function for protected operations.
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(swagger, &nethttpmiddleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: deps.Authenticator.Authenticate,
			// Keep the schema and the offending value out of error messages, so
			// the problem detail is one line and never echoes the spec back.
			SchemaValidationOptions: []openapi3.SchemaValidationOption{openapi3.DisableSchemaErrorDetails()},
		},
		// WithOpts, not the deprecated ErrorHandler: only this form receives the
		// request, without which the problem response carries no trace ID.
		ErrorHandlerWithOpts: validationErrorHandler,
	})

	for _, mount := range deps.Mounts {
		mount(r)
	}

	if deps.API == nil {
		deps.API = api.Unimplemented{}
	}
	// The generated wrapper applies Middlewares last-to-first, so the
	// single-document check sees the raw body before the validator re-encodes it.
	api.HandlerWithOptions(deps.API, api.ChiServerOptions{
		BaseRouter:       r,
		Middlewares:      []api.MiddlewareFunc{validator, SingleJSONDocument},
		ErrorHandlerFunc: paramErrorHandler,
	})

	return r, nil
}

// validationErrorHandler writes the validator's failures as problem responses.
func validationErrorHandler(_ context.Context, err error, w http.ResponseWriter, r *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
	// SingleJSONDocument already turns an oversized JSON body into a 413. Any
	// other body is first read by the security check, which buffers it before
	// authenticating, so a chunked one over MaxBytes surfaces as an
	// authentication failure. It is a size problem, not a credentials one.
	if mbe := new(http.MaxBytesError); errors.As(err, &mbe) {
		problem.Status(w, r, http.StatusRequestEntityTooLarge, "request body exceeds the size limit")
		return
	}
	if opts.StatusCode == http.StatusUnauthorized {
		// RFC 9110 15.5.2 requires a challenge on every 401; RFC 6750 section 3
		// adds error="invalid_token" when credentials were sent but rejected.
		challenge := `Bearer realm="forge"`
		if r.Header.Get("Authorization") != "" {
			challenge += `, error="invalid_token"`
		}
		w.Header().Set("WWW-Authenticate", challenge)
	}
	problem.Status(w, r, opts.StatusCode, err.Error())
}

func paramErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	problem.Status(w, r, http.StatusBadRequest, err.Error())
}
