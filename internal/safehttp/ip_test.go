package safehttp

import (
	"net/netip"
	"testing"
)

func TestIPClassifierTable(t *testing.T) {
	tests := []struct {
		name          string
		ip            string
		allowLoopback bool
		allowPrivate  bool
		wantBlocked   bool
	}{
		// Public IPv4 addresses
		{"public 1.1.1.1", "1.1.1.1", false, false, false},
		{"public 8.8.8.8", "8.8.8.8", false, false, false},
		{"public 93.184.216.34", "93.184.216.34", false, false, false},
		{"public near CGNAT lower 100.63.255.255", "100.63.255.255", false, false, false},
		{"public near CGNAT upper 100.128.0.1", "100.128.0.1", false, false, false},
		{"public near benchmark lower 198.17.255.255", "198.17.255.255", false, false, false},
		{"public near benchmark upper 198.20.0.1", "198.20.0.1", false, false, false},

		// Public IPv6 addresses
		{"public Cloudflare DNS 2606:4700:4700::1111", "2606:4700:4700::1111", false, false, false},
		{"public Google DNS 2001:4860:4860::8888", "2001:4860:4860::8888", false, false, false},

		// Loopback IPv4 (127.0.0.0/8)
		{"loopback 127.0.0.1 default", "127.0.0.1", false, false, true},
		{"loopback 127.0.0.1 allowed", "127.0.0.1", true, false, false},
		{"loopback 127.1.2.3 default", "127.1.2.3", false, false, true},
		{"loopback 127.255.255.254 default", "127.255.255.254", false, false, true},

		// Loopback IPv6 (::1)
		{"loopback ::1 default", "::1", false, false, true},
		{"loopback ::1 allowed", "::1", true, false, false},

		// RFC 1918 Private IPv4 (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
		{"private 10.0.0.1 default", "10.0.0.1", false, false, true},
		{"private 10.0.0.1 allowed", "10.0.0.1", false, true, false},
		{"private 10.255.255.254 default", "10.255.255.254", false, false, true},
		{"private 172.16.0.1 default", "172.16.0.1", false, false, true},
		{"private 172.16.0.1 allowed", "172.16.0.1", false, true, false},
		{"private 172.31.255.254 default", "172.31.255.254", false, false, true},
		{"private 192.168.1.1 default", "192.168.1.1", false, false, true},
		{"private 192.168.1.1 allowed", "192.168.1.1", false, true, false},

		// IPv6 Unique Local Address (fc00::/7)
		{"ULA fc00::1 default", "fc00::1", false, false, true},
		{"ULA fc00::1 allowed", "fc00::1", false, true, false},
		{"ULA fd12:3456:789a::1 default", "fd12:3456:789a::1", false, false, true},
		{"ULA fd12:3456:789a::1 allowed", "fd12:3456:789a::1", false, true, false},

		// Link-Local IPv4 (169.254.0.0/16 incl cloud metadata 169.254.169.254)
		{"metadata 169.254.169.254 default", "169.254.169.254", false, false, true},
		{"metadata 169.254.169.254 even with loopback allowed", "169.254.169.254", true, false, true},
		{"metadata 169.254.169.254 even with private allowed", "169.254.169.254", false, true, true},
		{"link-local 169.254.1.1", "169.254.1.1", false, false, true},
		{"link-local 169.254.255.254", "169.254.255.254", false, false, true},

		// Link-Local IPv6 (fe80::/10)
		{"link-local fe80::1", "fe80::1", false, false, true},
		{"link-local febf::1", "febf::1", false, false, true},

		// Unspecified addresses
		{"unspecified 0.0.0.0", "0.0.0.0", false, false, true},
		{"unspecified ::", "::", false, false, true},

		// 0.0.0.0/8 (This host on this network)
		{"0.0.0.0/8 range 0.0.0.1", "0.0.0.1", false, false, true},
		{"0.0.0.0/8 range 0.255.255.255", "0.255.255.255", false, false, true},

		// Multicast IPv4 (224.0.0.0/4) and IPv6 (ff00::/8)
		{"multicast 224.0.0.1", "224.0.0.1", false, false, true},
		{"multicast 239.255.255.250", "239.255.255.250", false, false, true},
		{"multicast ff02::1", "ff02::1", false, false, true},
		{"multicast ff05::2", "ff05::2", false, false, true},

		// Limited broadcast IPv4
		{"broadcast 255.255.255.255", "255.255.255.255", false, false, true},

		// CGNAT (100.64.0.0/10)
		{"CGNAT 100.64.0.1", "100.64.0.1", false, false, true},
		{"CGNAT 100.127.255.254", "100.127.255.254", false, false, true},

		// 192.0.0.0/24 (IETF Protocol Assignments)
		{"192.0.0.0/24 range 192.0.0.1", "192.0.0.1", false, false, true},

		// 198.18.0.0/15 (Benchmarking)
		{"benchmark 198.18.0.1", "198.18.0.1", false, false, true},
		{"benchmark 198.19.255.254", "198.19.255.254", false, false, true},

		// Documentation ranges IPv4 (RFC 5737) and IPv6 (RFC 3849)
		{"TEST-NET-1 192.0.2.1", "192.0.2.1", false, false, true},
		{"TEST-NET-2 198.51.100.1", "198.51.100.1", false, false, true},
		{"TEST-NET-3 203.0.113.1", "203.0.113.1", false, false, true},
		{"doc IPv6 2001:db8::1", "2001:db8::1", false, false, true},

		// Other reserved IPv4 / IPv6
		{"Class E 240.0.0.1", "240.0.0.1", false, false, true},
		{"6to4 relay 192.88.99.1", "192.88.99.1", false, false, true},
		{"IPv6 discard 100::1", "100::1", false, false, true},
		{"IPv6 benchmark 2001:2::1", "2001:2::1", false, false, true},
		{"IPv6 site-local fec0::1", "fec0::1", false, false, true},
		{"6to4 2002::1", "2002::1", false, false, true},

		// IPv4-mapped IPv6 (::ffff:0:0/96)
		{"::ffff:127.0.0.1 default", "::ffff:127.0.0.1", false, false, true},
		{"::ffff:127.0.0.1 loopback allowed", "::ffff:127.0.0.1", true, false, false},
		{"::ffff:10.0.0.1 default", "::ffff:10.0.0.1", false, false, true},
		{"::ffff:10.0.0.1 private allowed", "::ffff:10.0.0.1", false, true, false},
		{"::ffff:169.254.169.254", "::ffff:169.254.169.254", false, false, true},
		{"::ffff:8.8.8.8 public", "::ffff:8.8.8.8", false, false, false},

		// NAT64 Well-Known Prefix (64:ff9b::/96)
		{"NAT64 loopback 64:ff9b::127.0.0.1 default", "64:ff9b::127.0.0.1", false, false, true},
		{"NAT64 loopback 64:ff9b::127.0.0.1 loopback allowed", "64:ff9b::127.0.0.1", true, false, false},
		{"NAT64 private 64:ff9b::10.0.0.1 default", "64:ff9b::10.0.0.1", false, false, true},
		{"NAT64 private 64:ff9b::10.0.0.1 private allowed", "64:ff9b::10.0.0.1", false, true, false},
		{"NAT64 metadata 64:ff9b::169.254.169.254", "64:ff9b::169.254.169.254", false, false, true},
		{"NAT64 public 64:ff9b::8.8.8.8", "64:ff9b::8.8.8.8", false, false, false},

		// SIIT IPv4-translated (::ffff:0:0/96)
		{"SIIT loopback ::ffff:0:127.0.0.1 default", "::ffff:0:127.0.0.1", false, false, true},
		{"SIIT loopback ::ffff:0:127.0.0.1 allowed", "::ffff:0:127.0.0.1", true, false, false},
		{"SIIT metadata ::ffff:0:169.254.169.254", "::ffff:0:169.254.169.254", false, false, true},
		{"SIIT public ::ffff:0:8.8.8.8", "::ffff:0:8.8.8.8", false, false, false},

		// IPv4-compatible IPv6 (::/96 deprecated)
		{"Compat loopback ::127.0.0.1 default", "::127.0.0.1", false, false, true},
		{"Compat metadata ::169.254.169.254", "::169.254.169.254", false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.ip)
			opts := Options{
				AllowLoopback: tt.allowLoopback,
				AllowPrivate:  tt.allowPrivate,
			}
			got := isBlocked(addr, opts)
			if got != tt.wantBlocked {
				t.Errorf("isBlocked(%q, loopback=%v, private=%v) = %v; want %v",
					tt.ip, tt.allowLoopback, tt.allowPrivate, got, tt.wantBlocked)
			}
		})
	}
}
