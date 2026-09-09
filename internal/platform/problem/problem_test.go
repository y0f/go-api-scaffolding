package problem

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) Problem {
	t.Helper()
	var p Problem
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return p
}

func TestStatusWritesRFC9457Document(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/widgets/abc", nil)
	Status(rec, r, http.StatusNotFound, "widget not found")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("content type = %q, want %q", ct, ContentType)
	}
	p := decode(t, rec)
	want := Problem{Type: "about:blank", Title: "Not Found", Status: 404, Detail: "widget not found", Instance: "/v1/widgets/abc"}
	if p != want {
		t.Errorf("problem = %+v, want %+v", p, want)
	}
}

func TestWriteKeepsAnExplicitInstance(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/widgets", nil)
	p := New(http.StatusConflict, "already exists")
	p.Instance = "/v1/widgets/existing"
	p.Write(rec, r)

	if got := decode(t, rec).Instance; got != "/v1/widgets/existing" {
		t.Errorf("instance = %q, want the explicit value", got)
	}
}

func TestWriteAttachesTheActiveTraceID(t *testing.T) {
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
	})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(trace.ContextWithSpanContext(r.Context(), sc))
	rec := httptest.NewRecorder()
	Status(rec, r, http.StatusInternalServerError, "boom")

	if got := decode(t, rec).TraceID; got != traceID.String() {
		t.Errorf("traceId = %q, want %q", got, traceID.String())
	}
}

func TestWriteOmitsTraceIDWithoutASpan(t *testing.T) {
	rec := httptest.NewRecorder()
	Status(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusBadRequest, "bad")

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["traceId"]; present {
		t.Error("traceId should be omitted when no span is active")
	}
}
