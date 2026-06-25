// Package server wires the HTTP listener, middleware, and graceful shutdown.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/y0f/go-api-scaffolding/internal/config"
)

// Run starts the HTTP server and blocks until ctx is cancelled, then performs a
// readiness-first graceful drain: flip readiness so load balancers stop
// routing, pause, then stop accepting and wait for in-flight requests.
func Run(ctx context.Context, cfg config.HTTPConfig, handler http.Handler, health *Health, logger *slog.Logger) error {
	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining", slog.Duration("drain_delay", cfg.DrainDelay))
		health.SetReady(false)
		time.Sleep(cfg.DrainDelay)

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		logger.Info("http server stopped cleanly")
		return nil
	}
}
