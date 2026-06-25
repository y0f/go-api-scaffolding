package server

import (
	"crypto/subtle"
	"expvar"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"time"

	"github.com/y0f/go-api-scaffolding/internal/config"
)

// NewAdminServer builds the introspection listener exposing pprof and expvar.
// It is intentionally separate from the public server and, when a token is set,
// requires it as a bearer credential. Bind it to localhost in production.
func NewAdminServer(cfg config.AdminConfig) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/vars", expvar.Handler())

	return &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler:           tokenGuard(cfg.Token, mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// tokenGuard requires the configured bearer token, compared in constant time.
// An empty token fails closed: every request is rejected.
func tokenGuard(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := bearerToken(r.Header.Get("Authorization"))
		if token == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) > len(prefix) && header[:len(prefix)] == prefix {
		return header[len(prefix):]
	}
	return ""
}
