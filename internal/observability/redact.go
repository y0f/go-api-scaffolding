package observability

import (
	"context"
	"log/slog"
	"strings"
)

// sensitiveKeys are matched case-insensitively as substrings, so "user_password"
// and "X-Auth-Token" are both caught. Extend this list for your domain.
var sensitiveKeys = []string{
	"password", "passwd", "secret", "token", "authorization",
	"api_key", "apikey", "access_key", "cookie",
	"ssn", "credit_card", "card_number", "cvv",
}

const redactedValue = "REDACTED"

// RedactingHandler wraps a slog.Handler and replaces the values of attributes
// whose key looks sensitive, recursing into groups and slog.LogValuer values
// (which resolve to groups). It guards against logging
// credentials by accident, which is the most common log-hygiene failure.
type RedactingHandler struct {
	next slog.Handler
}

func NewRedactingHandler(next slog.Handler) *RedactingHandler {
	return &RedactingHandler{next: next}
}

func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	// Most records carry nothing to redact; pass those through rather than
	// copying every attr into a new record on the hot path.
	clean := true
	r.Attrs(func(a slog.Attr) bool {
		clean = !needsRedaction(a)
		return clean
	})
	if clean {
		return h.next.Handle(ctx, r)
	}
	clone := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clone.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, clone)
}

func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a)
	}
	return &RedactingHandler{next: h.next.WithAttrs(out)}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{next: h.next.WithGroup(name)}
}

// needsRedaction reports whether redactAttr could change a. LogValuers are
// not resolved here, so they always count.
func needsRedaction(a slog.Attr) bool {
	switch a.Value.Kind() {
	case slog.KindLogValuer:
		return true
	case slog.KindGroup:
		for _, ga := range a.Value.Group() {
			if needsRedaction(ga) {
				return true
			}
		}
		return false
	default:
		return isSensitive(a.Key)
	}
}

func redactAttr(a slog.Attr) slog.Attr {
	// A LogValuer (such as a struct with a LogValue method) may resolve to a
	// group holding sensitive keys, so resolve it before inspecting it.
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		out := make([]slog.Attr, len(group))
		for i, ga := range group {
			out[i] = redactAttr(ga)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	}
	if isSensitive(a.Key) {
		return slog.String(a.Key, redactedValue)
	}
	return a
}

func isSensitive(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}
