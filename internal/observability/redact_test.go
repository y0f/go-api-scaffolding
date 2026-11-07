package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

func TestRedactingHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&buf, nil)))

	logger.Info("login attempt",
		slog.String("password", "hunter2"),
		slog.String("user", "alice"),
		slog.Group("credentials", slog.String("api_token", "abc123")),
	)

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if got["password"] != redactedValue {
		t.Errorf("password = %v, want redacted", got["password"])
	}
	if got["user"] != "alice" {
		t.Errorf("user = %v, want alice", got["user"])
	}
	credentials, ok := got["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credentials group missing: %v", got["credentials"])
	}
	if credentials["api_token"] != redactedValue {
		t.Errorf("nested api_token = %v, want redacted", credentials["api_token"])
	}
}

type loginUser struct{}

func (loginUser) LogValue() slog.Value {
	return slog.GroupValue(slog.String("name", "alice"), slog.String("password", "hunter2"))
}

// The LogValuer is logged under a non-sensitive key, so only resolving it
// exposes the sensitive key inside.
func TestRedactingHandlerResolvesLogValuer(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&buf, nil)))

	logger.Info("login", slog.Any("user", loginUser{}))
	logger.With(slog.Any("user", loginUser{})).Info("again")

	out := buf.String()
	if strings.Contains(out, "hunter2") {
		t.Errorf("password logged in clear text:\n%s", out)
	}
	if !strings.Contains(out, `"name":"alice"`) {
		t.Errorf("non-sensitive field of the LogValuer lost:\n%s", out)
	}
}

func TestRedactingHandlerRedactsSensitiveGroups(t *testing.T) {
	tests := []struct {
		name string
		log  func(*slog.Logger)
	}{
		{"record group", func(logger *slog.Logger) {
			logger.Info("request", slog.Group("authorization", slog.String("value", "private-credential")))
		}},
		{"bound group", func(logger *slog.Logger) {
			logger.With(slog.Group("secrets", slog.String("value", "private-credential"))).Info("request")
		}},
		{"inherited group", func(logger *slog.Logger) {
			logger.WithGroup("authorization").Info("request", slog.String("value", "private-credential"))
		}},
		{"nested inherited group", func(logger *slog.Logger) {
			logger.WithGroup("secrets").WithGroup("nested").With(slog.String("value", "private-credential")).Info("request")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&buf, nil)))
			tt.log(logger.With(slog.String("user", "alice")))
			out := buf.String()
			if strings.Contains(out, "private-credential") || !strings.Contains(out, redactedValue) {
				t.Fatalf("sensitive group leaked its value: %s", out)
			}
			if !strings.Contains(out, `"user":"alice"`) {
				t.Fatalf("non-sensitive parent attribute was lost: %s", out)
			}
		})
	}
}

func TestNeedsRedaction(t *testing.T) {
	tests := []struct {
		attr slog.Attr
		want bool
	}{
		{slog.String("method", "GET"), false},
		{slog.String("Authorization", "Bearer x"), true},
		{slog.Group("req", slog.String("path", "/")), false},
		{slog.Group("req", slog.Group("hdr", slog.String("cookie", "c"))), true},
		{slog.Group("authorization", slog.String("value", "private-credential")), true},
		{slog.Any("user", loginUser{}), true},
	}
	for _, tt := range tests {
		if got := needsRedaction(tt.attr); got != tt.want {
			t.Errorf("needsRedaction(%v) = %v, want %v", tt.attr, got, tt.want)
		}
	}
}

// The access log line passes through the full handler chain on every request;
// with nothing to redact, the only record copy should be TraceHandler's.
func BenchmarkAccessLogChain(b *testing.B) {
	// A real handler, not slog.DiscardHandler: that one reports disabled, so the
	// logger would skip the chain and the benchmark would measure nothing.
	logger := slog.New(NewTraceHandler(NewRedactingHandler(slog.NewJSONHandler(io.Discard, nil)))) //nolint:sloglint // see above
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
	}))
	b.ReportAllocs()
	for b.Loop() {
		logger.InfoContext(ctx, "http request",
			slog.String("method", "GET"), slog.String("path", "/v1/widgets"),
			slog.Int("status", 200), slog.Int("bytes", 123),
			slog.Duration("duration", time.Millisecond), slog.String("request_id", "abc"),
			slog.String("remote_ip", "127.0.0.1"))
	}
}
