// Package problem renders errors as RFC 9457 problem details
// (application/problem+json), attaching the active trace ID so a failed
// response can be traced back to its span.
package problem

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

const ContentType = "application/problem+json"

// Problem is an RFC 9457 problem details object.
type Problem struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Detail   string       `json:"detail,omitempty"`
	Instance string       `json:"instance,omitempty"`
	TraceID  string       `json:"traceId,omitempty"`
	Errors   []FieldError `json:"errors,omitempty"`
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// New builds a problem with type "about:blank" and the standard title for the
// status code, per RFC 9457 section 4.2.1.
func New(status int, detail string) *Problem {
	return &Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	}
}

func (p *Problem) WithType(uri, title string) *Problem {
	p.Type = uri
	p.Title = title
	return p
}

func (p *Problem) WithField(field, message string) *Problem {
	p.Errors = append(p.Errors, FieldError{Field: field, Message: message})
	return p
}

// Write renders the problem, setting the trace ID from the request context and
// defaulting the instance to the request path.
func (p *Problem) Write(w http.ResponseWriter, r *http.Request) {
	if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
		p.TraceID = sc.TraceID().String()
	}
	if p.Instance == "" {
		p.Instance = r.URL.Path
	}
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(p.Status)
	if err := json.NewEncoder(w).Encode(p); err != nil {
		slog.ErrorContext(r.Context(), "encode problem response", slog.Any("error", err))
	}
}

// Status is a convenience for writing a problem from just a status and detail.
func Status(w http.ResponseWriter, r *http.Request, status int, detail string) {
	New(status, detail).Write(w, r)
}
