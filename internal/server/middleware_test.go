package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientIPStripsPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.5:54321"
	if got := clientIP(r); got != "203.0.113.5" {
		t.Errorf("clientIP = %q, want 203.0.113.5", got)
	}
}

func TestRateLimitIsPerIPNotPerConnection(t *testing.T) {
	// A near-zero refill rate keeps the assertion independent of test timing.
	handler := RateLimit(0.001, 1)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	call := func(port string) int {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "198.51.100.7:" + port
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}

	if code := call("1000"); code != http.StatusOK {
		t.Fatalf("first request code = %d, want 200", code)
	}
	// Same IP, different source port must share the bucket and be limited.
	if code := call("2000"); code != http.StatusTooManyRequests {
		t.Fatalf("second request from same IP code = %d, want 429", code)
	}
}

func TestMaxBytesRejectsDeclaredOversize(t *testing.T) {
	handler := MaxBytes(16)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", 100)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestMaxBytesCapsUndeclaredBody(t *testing.T) {
	var readErr error
	handler := MaxBytes(16)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", 100)))
	req.ContentLength = -1 // unknown length (chunked); the read cap must still apply
	handler.ServeHTTP(httptest.NewRecorder(), req)

	var tooLarge *http.MaxBytesError
	if !errors.As(readErr, &tooLarge) {
		t.Fatalf("expected *http.MaxBytesError, got %v", readErr)
	}
}

func TestAccessLogRecordsClientIPWithoutPort(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := AccessLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodGet, "/v1/widgets", nil)
	r.RemoteAddr = "203.0.113.5:54321"
	handler.ServeHTTP(httptest.NewRecorder(), r)

	var line struct {
		RemoteIP string `json:"remote_ip"`
		Status   int    `json:"status"`
	}
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("decode log line %q: %v", buf.String(), err)
	}
	if line.RemoteIP != "203.0.113.5" || line.Status != http.StatusOK {
		t.Errorf("logged remote_ip=%q status=%d, want 203.0.113.5/200", line.RemoteIP, line.Status)
	}
}

func TestSingleJSONDocument(t *testing.T) {
	cases := []struct {
		name, contentType, body string
		want                    int
		wantBody                string
	}{
		{"one object", "application/json", `{"name":"a"}`, http.StatusOK, `{"name":"a"}`},
		{"one object with whitespace", "application/json; charset=utf-8", " {\"name\":\"a\"}\n", http.StatusOK, " {\"name\":\"a\"}\n"},
		{"problem+json counts as JSON", "application/problem+json", `{}{}`, http.StatusBadRequest, ""},
		{"two objects", "application/json", `{"name":"a"}{"name":"b"}`, http.StatusBadRequest, ""},
		{"trailing garbage", "application/json", `{"name":"a"}xyz`, http.StatusBadRequest, ""},
		{"malformed", "application/json", `{"name":`, http.StatusBadRequest, ""},
		{"empty body is left to the validator", "application/json", ``, http.StatusOK, ``},
		{"not JSON is left alone", "text/plain", `a}b`, http.StatusOK, `a}b`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			h := SingleJSONDocument(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				got = string(b)
			}))
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusOK && got != tc.wantBody {
				t.Errorf("next saw body %q, want %q", got, tc.wantBody)
			}
		})
	}
}

func TestSingleJSONDocumentOversizeIs413(t *testing.T) {
	h := MaxBytes(8)(SingleJSONDocument(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next must not run")
	})))
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"too long"}`))
	r.ContentLength = -1 // chunked: only the read can catch it
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}
