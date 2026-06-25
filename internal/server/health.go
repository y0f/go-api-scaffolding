package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Health answers Kubernetes-style probes. Readiness is flipped off at the start
// of graceful shutdown so a load balancer stops routing before in-flight
// requests are drained.
type Health struct {
	ready atomic.Bool
	pool  *pgxpool.Pool
}

func NewHealth(pool *pgxpool.Pool) *Health {
	h := &Health{pool: pool}
	h.ready.Store(true)
	return h
}

func (h *Health) SetReady(ready bool) { h.ready.Store(ready) }

// Livez reports process liveness independent of dependencies.
func (h *Health) Livez(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Readyz reports whether the service should receive traffic: it must be marked
// ready and the database must answer a ping.
func (h *Health) Readyz(w http.ResponseWriter, r *http.Request) {
	if !h.ready.Load() {
		http.Error(w, "draining", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.pool.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}
