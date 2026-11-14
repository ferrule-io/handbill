package ratelimit

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP returns the address of the client that sent r. Without trusted
// proxies it is RemoteAddr's. When RemoteAddr is a trusted proxy, the
// X-Forwarded-For chain is walked from the right (the hop the proxy
// appended) past every trusted proxy; the first untrusted hop is the
// client. Entries left of it are whatever the client sent and are never
// believed. An unparsable entry stops the walk at the last trusted hop.
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	remote := parseAddr(r.RemoteAddr)
	if !remote.IsValid() || !isTrusted(remote, trusted) {
		return remote
	}
	hops := r.Header.Values("X-Forwarded-For")
	client := remote
	for i := len(hops) - 1; i >= 0; i-- {
		parts := strings.Split(hops[i], ",")
		for j := len(parts) - 1; j >= 0; j-- {
			a := parseAddr(strings.TrimSpace(parts[j]))
			if !a.IsValid() {
				return client
			}
			client = a
			if !isTrusted(a, trusted) {
				return a
			}
		}
	}
	return client
}

// parseAddr accepts "ip", "ip:port", and "[ipv6]:port"; IPv4-mapped IPv6
// addresses are unmapped.
func parseAddr(s string) netip.Addr {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap().WithZone("")
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ipKey is a bucket key for a client address. IPv6 clients are grouped by
// /64, the smallest block a subscriber is usually assigned.
func ipKey(a netip.Addr) string {
	if !a.IsValid() {
		return "ip:unknown"
	}
	if a.Is6() {
		return "ip:" + netip.PrefixFrom(a, 64).Masked().String()
	}
	return "ip:" + a.String()
}
