package safehttp

import (
	"net/netip"
)

var (
	// IPv4 blocked prefixes
	prefix0_8        = netip.MustParsePrefix("0.0.0.0/8")          // RFC 1122 Current network ("this" host on this network)
	prefix10_8       = netip.MustParsePrefix("10.0.0.0/8")         // RFC 1918 Private-Use
	prefixCGNAT      = netip.MustParsePrefix("100.64.0.0/10")      // RFC 6598 Shared Address Space (CGNAT)
	prefixLoopback4  = netip.MustParsePrefix("127.0.0.0/8")        // RFC 1122 Loopback
	prefixLinkLocal4 = netip.MustParsePrefix("169.254.0.0/16")     // RFC 3927 Link-Local (incl. cloud metadata 169.254.169.254)
	prefix172_12     = netip.MustParsePrefix("172.16.0.0/12")      // RFC 1918 Private-Use
	prefix192_0_0_24 = netip.MustParsePrefix("192.0.0.0/24")       // RFC 6890 IETF Protocol Assignments
	prefixTestNet1   = netip.MustParsePrefix("192.0.2.0/24")       // RFC 5737 Documentation (TEST-NET-1)
	prefix6to4Relay  = netip.MustParsePrefix("192.88.99.0/24")     // RFC 7526 6to4 Relay Anycast (deprecated)
	prefix192_168_16 = netip.MustParsePrefix("192.168.0.0/16")     // RFC 1918 Private-Use
	prefix198_18_15  = netip.MustParsePrefix("198.18.0.0/15")      // RFC 2544 / RFC 6890 Benchmarking
	prefixTestNet2   = netip.MustParsePrefix("198.51.100.0/24")    // RFC 5737 Documentation (TEST-NET-2)
	prefixTestNet3   = netip.MustParsePrefix("203.0.113.0/24")     // RFC 5737 Documentation (TEST-NET-3)
	prefixMulticast4 = netip.MustParsePrefix("224.0.0.0/4")        // RFC 5771 Multicast
	prefixClassE     = netip.MustParsePrefix("240.0.0.0/4")        // RFC 1112 Reserved for future use
	prefixBroadcast4 = netip.MustParsePrefix("255.255.255.255/32") // RFC 919 Limited Broadcast

	// IPv6 blocked prefixes
	prefixDiscard6   = netip.MustParsePrefix("100::/64")      // RFC 6666 Discard-Only Address Block
	prefixBench6     = netip.MustParsePrefix("2001:2::/48")   // RFC 5180 BMWG IPv6 Benchmarking
	prefixDoc6       = netip.MustParsePrefix("2001:db8::/32") // RFC 3849 Documentation
	prefix6to4       = netip.MustParsePrefix("2002::/16")     // RFC 3056 6to4 (embeds v4)
	prefixULA6       = netip.MustParsePrefix("fc00::/7")      // RFC 4193 Unique Local Address
	prefixLinkLocal6 = netip.MustParsePrefix("fe80::/10")     // RFC 4291 Link-Local Unicast
	prefixSiteLocal6 = netip.MustParsePrefix("fec0::/10")     // RFC 3879 Site-Local (deprecated)
	prefixMulticast6 = netip.MustParsePrefix("ff00::/8")      // RFC 4291 Multicast

	// IPv4/IPv6 translation / mapping prefixes
	prefixNAT64WKP = netip.MustParsePrefix("64:ff9b::/96")    // RFC 6052 Well-Known Prefix
	prefixSIIT     = netip.MustParsePrefix("::ffff:0:0:0/96") // RFC 2765 SIIT translated
	prefixCompat6  = netip.MustParsePrefix("::/96")           // RFC 4291 IPv4-compatible (deprecated)
)

func isBlocked(addr netip.Addr, opts Options) bool {
	if !addr.IsValid() {
		return true
	}

	// 1. Unmap or translate embedded IPv4 addresses.
	if addr.Is4In6() {
		// ::ffff:x.x.x.x
		return isBlockedV4(addr.Unmap(), opts)
	}

	if addr.Is6() {
		// NAT64 Well-Known Prefix 64:ff9b::/96
		if prefixNAT64WKP.Contains(addr) {
			b := addr.As16()
			v4 := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
			return isBlockedV4(v4, opts)
		}

		// SIIT IPv4-translated ::ffff:0:0/96
		if prefixSIIT.Contains(addr) {
			b := addr.As16()
			v4 := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
			return isBlockedV4(v4, opts)
		}

		// 6to4 2002::/16 embeds IPv4 in bytes 2..5
		if prefix6to4.Contains(addr) {
			b := addr.As16()
			v4 := netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})
			if isBlockedV4(v4, opts) {
				return true
			}
			return true // 6to4 is deprecated and blocked
		}

		// IPv4-compatible IPv6 ::x.x.x.x (deprecated RFC 4291)
		// Check if it's in ::/96 but not :: or ::1
		if prefixCompat6.Contains(addr) {
			if addr.IsUnspecified() {
				return true
			}
			if addr.IsLoopback() {
				return !opts.AllowLoopback
			}
			b := addr.As16()
			v4 := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
			return isBlockedV4(v4, opts)
		}

		// Native IPv6 checks:
		if addr.IsUnspecified() {
			return true
		}
		if addr.IsLoopback() {
			return !opts.AllowLoopback
		}
		if prefixULA6.Contains(addr) { // fc00::/7
			return !opts.AllowPrivate
		}
		if prefixLinkLocal6.Contains(addr) || addr.IsLinkLocalUnicast() { // fe80::/10
			return true
		}
		if prefixSiteLocal6.Contains(addr) { // fec0::/10
			return true
		}
		if prefixMulticast6.Contains(addr) || addr.IsMulticast() { // ff00::/8
			return true
		}
		if prefixDoc6.Contains(addr) { // 2001:db8::/32
			return true
		}
		if prefixDiscard6.Contains(addr) { // 100::/64
			return true
		}
		if prefixBench6.Contains(addr) { // 2001:2::/48
			return true
		}

		return false
	}

	return isBlockedV4(addr, opts)
}

func isBlockedV4(addr netip.Addr, opts Options) bool {
	if !addr.IsValid() {
		return true
	}
	if addr.IsUnspecified() || prefix0_8.Contains(addr) { // 0.0.0.0/8
		return true
	}
	if addr.IsLoopback() || prefixLoopback4.Contains(addr) { // 127.0.0.0/8
		return !opts.AllowLoopback
	}
	if prefix10_8.Contains(addr) || prefix172_12.Contains(addr) || prefix192_168_16.Contains(addr) { // RFC 1918
		return !opts.AllowPrivate
	}
	if prefixLinkLocal4.Contains(addr) || addr.IsLinkLocalUnicast() { // 169.254.0.0/16 incl 169.254.169.254
		return true
	}
	if prefixCGNAT.Contains(addr) { // 100.64.0.0/10
		return true
	}
	if prefix192_0_0_24.Contains(addr) { // 192.0.0.0/24
		return true
	}
	if prefixTestNet1.Contains(addr) { // 192.0.2.0/24
		return true
	}
	if prefix6to4Relay.Contains(addr) { // 192.88.99.0/24
		return true
	}
	if prefix198_18_15.Contains(addr) { // 198.18.0.0/15
		return true
	}
	if prefixTestNet2.Contains(addr) { // 198.51.100.0/24
		return true
	}
	if prefixTestNet3.Contains(addr) { // 203.0.113.0/24
		return true
	}
	if prefixMulticast4.Contains(addr) || addr.IsMulticast() { // 224.0.0.0/4
		return true
	}
	if prefixClassE.Contains(addr) { // 240.0.0.0/4
		return true
	}
	if prefixBroadcast4.Contains(addr) { // 255.255.255.255
		return true
	}

	return false
}
