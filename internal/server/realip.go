package server

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// RealIP resolves the client address behind trusted reverse proxies and
// rewrites r.RemoteAddr to it, so every later middleware (access log, rate
// limiter) keys on the client rather than the proxy. It runs first in the
// chain for that reason.
//
// Only X-Forwarded-For is consulted, and only when the connecting peer is
// inside one of the trusted prefixes; otherwise the header is ignored, so a
// client cannot spoof its address by sending the header directly. Walking the
// header from the right, the first hop that is not a trusted proxy is the
// client: entries left of it were supplied by the client or by proxies we do
// not control. An empty trusted list makes the middleware a pass-through.
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if len(trusted) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, ok := parseAddr(r.RemoteAddr)
			if ok && isTrusted(peer, trusted) {
				r.RemoteAddr = resolveClientIP(r, trusted)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// resolveClientIP returns the client address for r as a bare IP string. When
// the peer is not a trusted proxy, or the forwarded chain yields nothing
// usable, it falls back to the peer's own address.
func resolveClientIP(r *http.Request, trusted []netip.Prefix) string {
	peer, ok := parseAddr(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	if !isTrusted(peer, trusted) {
		return peer.String()
	}
	hops := forwardedFor(r.Header)
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := parseAddr(hops[i])
		if !ok {
			// A trusted proxy wrote something that is not an address; do not
			// guess, attribute the request to the proxy itself.
			return peer.String()
		}
		if !isTrusted(hop, trusted) {
			return hop.String()
		}
	}
	return peer.String()
}

// forwardedFor flattens every X-Forwarded-For header value, in order, into
// one list of trimmed hops. Proxies append, so the client is leftmost and the
// last proxy before us is rightmost.
func forwardedFor(h http.Header) []string {
	var hops []string
	for _, value := range h.Values("X-Forwarded-For") {
		for _, hop := range strings.Split(value, ",") {
			if hop = strings.TrimSpace(hop); hop != "" {
				hops = append(hops, hop)
			}
		}
	}
	return hops
}

// parseAddr accepts "ip", "ip:port" and "[ipv6]:port" and returns the address
// with any IPv4-mapped IPv6 form unmapped, so a mapped peer matches an IPv4
// prefix.
func parseAddr(s string) (netip.Addr, bool) {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
