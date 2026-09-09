package server

import (
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

func TestResolveClientIP(t *testing.T) {
	tests := []struct {
		name    string
		peer    string
		xff     []string
		trusted []string
		want    string
	}{
		{
			name: "no trusted proxies ignores the header",
			peer: "203.0.113.5:1234", xff: []string{"198.51.100.1"},
			want: "203.0.113.5",
		},
		{
			name: "untrusted peer ignores the header",
			peer: "203.0.113.5:1234", xff: []string{"198.51.100.1"}, trusted: []string{"10.0.0.0/8"},
			want: "203.0.113.5",
		},
		{
			name: "trusted peer takes the rightmost untrusted hop",
			peer: "10.0.0.2:1234", xff: []string{"198.51.100.1, 10.0.0.1"}, trusted: []string{"10.0.0.0/8"},
			want: "198.51.100.1",
		},
		{
			name: "client-supplied entries left of the real client are ignored",
			peer: "10.0.0.2:1234", xff: []string{"1.1.1.1, 198.51.100.1"}, trusted: []string{"10.0.0.0/8"},
			want: "198.51.100.1",
		},
		{
			name: "multiple header values are joined in order",
			peer: "10.0.0.2:1234", xff: []string{"1.1.1.1", "198.51.100.1, 10.0.0.1"}, trusted: []string{"10.0.0.0/8"},
			want: "198.51.100.1",
		},
		{
			name: "every hop trusted falls back to the peer",
			peer: "10.0.0.2:1234", xff: []string{"10.0.0.1"}, trusted: []string{"10.0.0.0/8"},
			want: "10.0.0.2",
		},
		{
			name: "trusted peer without header falls back to the peer",
			peer: "10.0.0.2:1234", trusted: []string{"10.0.0.0/8"},
			want: "10.0.0.2",
		},
		{
			name: "malformed hop falls back to the peer",
			peer: "10.0.0.2:1234", xff: []string{"not-an-ip"}, trusted: []string{"10.0.0.0/8"},
			want: "10.0.0.2",
		},
		{
			name: "ipv4-mapped ipv6 peer matches an ipv4 prefix",
			peer: "[::ffff:10.0.0.2]:1234", xff: []string{"198.51.100.1"}, trusted: []string{"10.0.0.0/8"},
			want: "198.51.100.1",
		},
		{
			name: "ipv6 hops are normalised",
			peer: "[fd00::1]:1234", xff: []string{"[2001:db8::1]:5555"}, trusted: []string{"fd00::/8"},
			want: "2001:db8::1",
		},
		{
			name: "peer without port is still matched",
			peer: "10.0.0.2", xff: []string{"198.51.100.1"}, trusted: []string{"10.0.0.0/8"},
			want: "198.51.100.1",
		},
		{
			name: "unparseable peer is returned verbatim",
			peer: "garbage", xff: []string{"198.51.100.1"}, trusted: []string{"10.0.0.0/8"},
			want: "garbage",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.peer
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := resolveClientIP(r, prefixes(t, tt.trusted...)); got != tt.want {
				t.Errorf("resolveClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRealIPRewritesRemoteAddrForTrustedPeer(t *testing.T) {
	var seen string
	handler := RealIP(prefixes(t, "10.0.0.0/8"))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "198.51.100.1" {
		t.Errorf("RemoteAddr = %q, want 198.51.100.1", seen)
	}
}

func TestRealIPLeavesRemoteAddrForUntrustedPeer(t *testing.T) {
	var seen string
	handler := RealIP(prefixes(t, "10.0.0.0/8"))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.5:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "203.0.113.5:1234" {
		t.Errorf("RemoteAddr = %q, want unchanged 203.0.113.5:1234", seen)
	}
}

func TestRealIPWithNoTrustedProxiesIsPassThrough(t *testing.T) {
	var seen string
	handler := RealIP(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "10.0.0.2:1234" {
		t.Errorf("RemoteAddr = %q, want unchanged 10.0.0.2:1234", seen)
	}
}

// The end-to-end property the feature exists for: behind a trusted proxy the
// limiter keys on the forwarded client, so two clients do not share a bucket.
func TestRateLimitBehindTrustedProxyKeysOnForwardedClient(t *testing.T) {
	handler := RealIP(prefixes(t, "10.0.0.0/8"))(RateLimit(1, 1)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
