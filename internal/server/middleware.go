package server

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/time/rate"

	"github.com/y0f/go-api-scaffolding/internal/platform/problem"
)

// AccessLog emits one structured line per request. Trace and span IDs are added
// automatically by the logger's handler.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			defer func() {
				logger.InfoContext(r.Context(), "http request",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", wrapped.Status()),
					slog.Int("bytes", wrapped.BytesWritten()),
					slog.Duration("duration", time.Since(start)),
					slog.String("request_id", middleware.GetReqID(r.Context())),
					slog.String("remote_ip", r.RemoteAddr),
				)
			}()
			next.ServeHTTP(wrapped, r)
		})
	}
}

// Recoverer converts a panic into a 500 problem response and logs the stack,
// keeping a single bad request from taking down the process.
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				logger.ErrorContext(r.Context(), "panic recovered",
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
				)
				problem.Status(w, r, http.StatusInternalServerError, "internal server error")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecureHeaders sets conservative security response headers on every response.
func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// CORS reflects allowed origins. With an empty allow-list it echoes any origin
// only when reflectAnyWhenEmpty is set (development); in production an empty list
// means no CORS headers are sent, so the policy fails closed.
func CORS(allowed []string, reflectAnyWhenEmpty bool) func(http.Handler) http.Handler {
	allowAny := reflectAnyWhenEmpty && len(allowed) == 0
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		set[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := set[origin]; ok || allowAny {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit applies a per-client-IP token bucket. Idle limiters are evicted so
// the map does not grow without bound. Behind a trusted proxy, add an
// X-Forwarded-For parser with an explicit trusted-proxy allow-list.
func RateLimit(perSecond float64, burst int) func(http.Handler) http.Handler {
	limiters := newIPLimiters(rate.Limit(perSecond), burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiters.get(clientIP(r)).Allow() {
				problem.Status(w, r, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP returns the host part of RemoteAddr, dropping the ephemeral source
// port so the limiter keys on the client address rather than the connection.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type ipLimiters struct {
	mu       sync.Mutex
	clients  map[string]*clientLimiter
	rate     rate.Limit
	burst    int
	lastSwep time.Time
}

type clientLimiter struct {
	limiter *rate.Limiter
	seen    time.Time
}

func newIPLimiters(r rate.Limit, burst int) *ipLimiters {
	return &ipLimiters{clients: make(map[string]*clientLimiter), rate: r, burst: burst, lastSwep: time.Now()}
}

func (l *ipLimiters) get(key string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.lastSwep) > time.Minute {
		for k, c := range l.clients {
			if now.Sub(c.seen) > 3*time.Minute {
				delete(l.clients, k)
			}
		}
		l.lastSwep = now
	}
	c, ok := l.clients[key]
	if !ok {
		c = &clientLimiter{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.clients[key] = c
	}
	c.seen = now
	return c.limiter
}
