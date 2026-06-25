// Command api is the service entrypoint. It loads configuration, wires
// dependencies, starts the HTTP server and the outbox relay, and shuts down
// gracefully on SIGINT or SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/y0f/go-api-scaffolding/internal/auth"
	"github.com/y0f/go-api-scaffolding/internal/config"
	"github.com/y0f/go-api-scaffolding/internal/idempotency"
	"github.com/y0f/go-api-scaffolding/internal/maintenance"
	"github.com/y0f/go-api-scaffolding/internal/modules/widget"
	"github.com/y0f/go-api-scaffolding/internal/observability"
	"github.com/y0f/go-api-scaffolding/internal/outbox"
	"github.com/y0f/go-api-scaffolding/internal/platform/database"
	"github.com/y0f/go-api-scaffolding/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("service exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := observability.NewLogger(observability.ParseLevel(cfg.Log.Level), cfg.Log.Format)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	telemetry, err := observability.Setup(ctx, observability.TelemetryConfig{
		ServiceName:    cfg.Telemetry.ServiceName,
		ServiceVersion: cfg.ServiceVersion(),
		Environment:    cfg.Env,
		OTLPEndpoint:   cfg.Telemetry.OTLPEndpoint,
		SampleRatio:    cfg.Telemetry.SampleRatio,
	})
	if err != nil {
		return err
	}
	defer shutdown(logger, "telemetry", telemetry.Shutdown)

	pool, err := database.NewPool(ctx, database.Config{
		URL:               cfg.Database.URL,
		MaxConns:          cfg.Database.MaxConns,
		MinConns:          cfg.Database.MinConns,
		MaxConnLifetime:   cfg.Database.MaxConnLifetime,
		MaxConnIdleTime:   cfg.Database.MaxConnIdleTime,
		HealthCheckPeriod: cfg.Database.HealthCheckPeriod,
		ConnectTimeout:    cfg.Database.ConnectTimeout,
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	verifier, devIssuer, err := auth.NewVerifier(ctx, auth.Settings{
		JWKSURL:       cfg.Auth.JWKSURL,
		PublicKeyPath: cfg.Auth.PublicKeyPath,
		Issuer:        cfg.Auth.Issuer,
		Audience:      cfg.Auth.Audience,
	}, !cfg.IsProduction())
	if err != nil {
		return err
	}
	if devIssuer != nil {
		token, mintErr := devIssuer.Mint("dev-user", []string{"admin"}, cfg.Auth.DevTokenTTL)
		if mintErr != nil {
			return mintErr
		}
		// Logged in the message, not an attribute, so the redaction handler does
		// not scrub it. Development only.
		logger.Warn("development auth uses an ephemeral key, do not use in production; bearer token: " + token)
	}
	authenticator := auth.NewAuthenticator(verifier)

	idemStore := idempotency.NewStore(pool, 24*time.Hour)
	widgetHandler := widget.NewHandler(
		widget.NewService(widget.NewRepository(pool), logger),
		idemStore,
	)

	relay := outbox.NewRelay(pool, outbox.LogPublisher{Logger: logger}, logger, 100, 2*time.Second)
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		if err := relay.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("outbox relay stopped", slog.Any("error", err))
		}
	}()

	const outboxRetention = 7 * 24 * time.Hour
	reaper := maintenance.NewReaper(logger, 15*time.Minute,
		maintenance.Task{Name: "idempotency-keys", Run: idemStore.PurgeExpired},
		maintenance.Task{Name: "outbox-messages", Run: func(c context.Context) (int64, error) {
			return relay.PurgePublished(c, outboxRetention)
		}},
	)
	reaperDone := make(chan struct{})
	go func() {
		defer close(reaperDone)
		if err := reaper.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("reaper stopped", slog.Any("error", err))
		}
	}()

	if cfg.Admin.Enabled {
		admin := server.NewAdminServer(cfg.Admin)
		go func() {
			logger.Info("admin server listening", slog.String("addr", admin.Addr))
			if err := admin.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("admin server stopped", slog.Any("error", err))
			}
		}()
		defer shutdown(logger, "admin server", admin.Shutdown)
	}

	health := server.NewHealth(pool)
	router, err := server.NewRouter(server.RouterDeps{
		Logger:        logger,
		Telemetry:     telemetry,
		Health:        health,
		Authenticator: authenticator,
		WidgetHandler: widgetHandler,
		Config: server.RouterConfig{
			CORSAllowedOrigins: cfg.HTTP.CORSAllowedOrigins,
			RateLimitPerSecond: cfg.HTTP.RateLimitPerSecond,
			RateLimitBurst:     cfg.HTTP.RateLimitBurst,
			MaxBodyBytes:       cfg.HTTP.MaxBodyBytes,
			Development:        !cfg.IsProduction(),
		},
	})
	if err != nil {
		return err
	}

	err = server.Run(ctx, cfg.HTTP, router, health, logger)

	// Wait for background workers to stop before the deferred pool.Close runs, so
	// none can run a query against a closed pool. Bound the wait so a stuck worker
	// cannot block shutdown indefinitely.
	deadline := time.After(5 * time.Second)
	for _, worker := range []struct {
		name string
		done <-chan struct{}
	}{{"outbox relay", relayDone}, {"reaper", reaperDone}} {
		select {
		case <-worker.done:
		case <-deadline:
			logger.Warn("worker did not stop within the shutdown grace period", slog.String("worker", worker.name))
		}
	}
	return err
}

func shutdown(logger *slog.Logger, name string, fn func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fn(ctx); err != nil {
		logger.Error("shutdown error", slog.String("component", name), slog.Any("error", err))
	}
}
