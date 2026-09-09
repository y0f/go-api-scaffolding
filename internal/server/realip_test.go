package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func prefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}

func TestRealIP(t *testing.T) {
	tests := []struct {
		name    string
		peer    string
		xff     []string
		trusted []string
		want    string
	}{
		{
			name: "no trusted proxies leaves the request untouched",
			peer: "203.0.113.5:1234", xff: []string{"198.51.100.1"},
			want: "203.0.113.5:1234",
		},
		{
			name: "untrusted peer leaves the request untouched",
			peer: "203.0.113.5:1234", xff: []string{"198.51.100.1"}, trusted: []string{"10.0.0.0/8"},
			want: "203.0.113.5:1234",
		},
		{
			name: "trusted peer takes the rightmost untrusted hop",
			peer: "10.0.0.2:1234", xff: []string{"198.51.100.1, 10.0.0.1"}, trusted: []string{"10.0.0.0/8"},
			want: "198.51.100.1:0",
		},
		{
			name: "client-supplied entries left of the real client are ignored",
			peer: "10.0.0.2:1234", xff: []string{"1.1.1.1, 198.51.100.1"}, trusted: []string{"10.0.0.0/8"},
			want: "198.51.100.1:0",
		},
		{
			name: "multiple header values are joined in order",
			peer: "10.0.0.2:1234", xff: []string{"1.1.1.1", "198.51.100.1, 10.0.0.1"}, trusted: []string{"10.0.0.0/8"},
			want: "198.51.100.1:0",
		},
		{
			name: "every hop trusted falls back to the peer",
			peer: "10.0.0.2:1234", xff: []string{"10.0.0.1"}, trusted: []string{"10.0.0.0/8"},
			want: "10.0.0.2:0",
		},
		{
			name: "trusted peer without header falls back to the peer",
			peer: "10.0.0.2:1234", trusted: []string{"10.0.0.0/8"},
			want: "10.0.0.2:0",
		},
		{
			name: "malformed hop falls back to the peer",
			peer: "10.0.0.2:1234", xff: []string{"not-an-ip"}, trusted: []string{"10.0.0.0/8"},
			want: "10.0.0.2:0",
		},
		{
			name: "ipv4-mapped ipv6 peer matches an ipv4 prefix",
			peer: "[::ffff:10.0.0.2]:1234", xff: []string{"198.51.100.1"}, trusted: []string{"10.0.0.0/8"},
			want: "198.51.100.1:0",
		},
		{
			name: "ipv6 client keeps the bracketed host:port shape",
			peer: "[fd00::1]:1234", xff: []string{"[2001:db8::1]:5555"}, trusted: []string{"fd00::/8"},
			want: "[2001:db8::1]:0",
		},
		{
			name: "peer without port is still matched",
			peer: "10.0.0.2", xff: []string{"198.51.100.1"}, trusted: []string{"10.0.0.0/8"},
			want: "198.51.100.1:0",
		},
		{
			name: "unparseable peer is left untouched",
			peer: "garbage", xff: []string{"198.51.100.1"}, trusted: []string{"10.0.0.0/8"},
			want: "garbage",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen string
			handler := RealIP(prefixes(t, tt.trusted...))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = r.RemoteAddr
			}))
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.peer
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			handler.ServeHTTP(httptest.NewRecorder(), r)
			if seen != tt.want {
				t.Errorf("RemoteAddr = %q, want %q", seen, tt.want)
			}
		})
	}
}

// Every reader in the chain splits RemoteAddr, so the rewritten value must
// survive net.SplitHostPort for IPv6 clients too.
func TestRealIPResultSplitsAsHostPort(t *testing.T) {
	var seen string
	handler := RealIP(prefixes(t, "fd00::/8"))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[fd00::1]:1234"
	r.Header.Set("X-Forwarded-For", "2001:db8::1")
	handler.ServeHTTP(httptest.NewRecorder(), r)
	host, _, err := net.SplitHostPort(seen)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", seen, err)
	}
	if host != "2001:db8::1" {
		t.Errorf("host = %q, want 2001:db8::1", host)
	}
}

// The end-to-end property the feature exists for: behind a trusted proxy the
// limiter keys on the forwarded client, so two clients do not share a bucket.
func TestRateLimitBehindTrustedProxyKeysOnForwardedClient(t *testing.T) {
	handler := RealIP(prefixes(t, "10.0.0.0/8"))(RateLimit(0.001, 1)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	call := func(client string) int {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "10.0.0.2:1234"
		r.Header.Set("X-Forwarded-For", client)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if code := call("198.51.100.1"); code != http.StatusOK {
		t.Fatalf("first client code = %d, want 200", code)
	}
	if code := call("198.51.100.2"); code != http.StatusOK {
		t.Fatalf("second client code = %d, want 200 (must not share the proxy's bucket)", code)
	}
	if code := call("198.51.100.1"); code != http.StatusTooManyRequests {
		t.Fatalf("first client again code = %d, want 429", code)
	}
}
