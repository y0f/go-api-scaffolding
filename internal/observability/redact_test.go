package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
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
