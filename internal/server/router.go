package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	api "github.com/y0f/go-api-scaffolding/internal/gen/api"
	"github.com/y0f/go-api-scaffolding/internal/observability"
	"github.com/y0f/go-api-scaffolding/internal/platform/problem"
)

type RouterConfig struct {
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
	WidgetHandler api.ServerInterface
	Config        RouterConfig
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
	r.Use(middleware.RequestID)
	r.Use(otelhttp.NewMiddleware("forge"))
	r.Use(SecureHeaders)
	r.Use(AccessLog(deps.Logger))
	r.Use(Recoverer(deps.Logger))
	r.Use(CORS(deps.Config.CORSAllowedOrigins, deps.Config.Development))
	r.Use(RateLimit(deps.Config.RateLimitPerSecond, deps.Config.RateLimitBurst))
	r.Use(MaxBytes(deps.Config.MaxBodyBytes))
	// Seed the principal holder before the validator runs authentication.
	r.Use(deps.Authenticator.SeedContext)

	r.Get("/livez", deps.Health.Livez)
	r.Get("/readyz", deps.Health.Readyz)
	r.Handle("/metrics", promhttp.HandlerFor(deps.Telemetry.Registry, promhttp.HandlerOpts{}))

	// The validator enforces the contract (params, bodies, enums) and runs the
	// security scheme's authentication function for protected operations.
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(swagger, &nethttpmiddleware.Options{
		Options:      openapi3filter.Options{AuthenticationFunc: deps.Authenticator.Authenticate},
		ErrorHandler: validationErrorHandler,
	})

	api.HandlerWithOptions(deps.WidgetHandler, api.ChiServerOptions{
		BaseRouter:       r,
		Middlewares:      []api.MiddlewareFunc{validator},
		ErrorHandlerFunc: paramErrorHandler,
	})

	return r, nil
}

func validationErrorHandler(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", problem.ContentType)
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(problem.New(statusCode, message))
}

func paramErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	problem.Status(w, r, http.StatusBadRequest, err.Error())
}
