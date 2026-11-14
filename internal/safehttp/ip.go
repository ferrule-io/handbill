package safehttp

import (
	"net/netip"
)

// isBlocked is default-deny: an address is allowed only when it is global
// unicast and outside every IANA special-purpose block that is not globally
// reachable (IANA IPv4/IPv6 Special-Purpose Address Registries). IPv6 must
// also be in 2000::/3, the only space IANA allocates for global unicast.
// Addresses that embed IPv4 (mapped, SIIT, compatible, and the NAT64
// well-known prefix 64:ff9b::/96, which IPv6-only hosts with DNS64 need)
// are judged by the embedded IPv4 address. Other translation and tunnel
// prefixes (64:ff9b:1::/48, Teredo, 6to4) are blocked outright: their
// embedding is not fixed or not worth trusting.
func isBlocked(addr netip.Addr, opts Options) bool {
	if !addr.IsValid() {
		return true
	}
	// Prefix.Contains never matches a zoned address.
	addr = addr.WithZone("")
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	if addr.Is4() {
		return isBlockedV4(addr, opts)
	}
	if addr.IsLoopback() { // ::1
		return !opts.AllowLoopback
	}
	if addr.IsUnspecified() { // ::
		return true
	}
	for _, p := range embedsV4 {
		if p.Contains(addr) {
			b := addr.As16()
			return isBlockedV4(netip.AddrFrom4([4]byte(b[12:])), opts)
		}
	}
	if prefixULA6.Contains(addr) {
		return !opts.AllowPrivate
	}
	if !addr.IsGlobalUnicast() || !prefixGlobal6.Contains(addr) {
		return true
	}
	for _, p := range blocked6 {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func isBlockedV4(addr netip.Addr, opts Options) bool {
	if prefixLoopback4.Contains(addr) {
		return !opts.AllowLoopback
	}
	for _, p := range private4 {
		if p.Contains(addr) {
			return !opts.AllowPrivate
		}
	}
	if !addr.IsGlobalUnicast() {
		return true
	}
	for _, p := range blocked4 {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

var (
	prefixLoopback4 = netip.MustParsePrefix("127.0.0.0/8") // RFC 1122 Loopback

	// RFC 1918 Private-Use, allowed only with AllowPrivate.
	private4 = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}

	// IPv4 special-purpose blocks not globally reachable (IsGlobalUnicast
	// already excludes loopback, link-local, multicast, and broadcast; they
	// are listed anyway so the list reads as the registry).
	blocked4 = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),          // RFC 791 "This network"
		netip.MustParsePrefix("100.64.0.0/10"),      // RFC 6598 Shared Address Space (CGNAT)
		netip.MustParsePrefix("169.254.0.0/16"),     // RFC 3927 Link-Local (incl. 169.254.169.254)
		netip.MustParsePrefix("192.0.0.0/24"),       // RFC 6890 IETF Protocol Assignments
		netip.MustParsePrefix("192.0.2.0/24"),       // RFC 5737 Documentation (TEST-NET-1)
		netip.MustParsePrefix("192.88.99.0/24"),     // RFC 7526 6to4 Relay Anycast (deprecated)
		netip.MustParsePrefix("198.18.0.0/15"),      // RFC 2544 Benchmarking
		netip.MustParsePrefix("198.51.100.0/24"),    // RFC 5737 Documentation (TEST-NET-2)
		netip.MustParsePrefix("203.0.113.0/24"),     // RFC 5737 Documentation (TEST-NET-3)
		netip.MustParsePrefix("224.0.0.0/4"),        // RFC 5771 Multicast
		netip.MustParsePrefix("240.0.0.0/4"),        // RFC 1112 Reserved
		netip.MustParsePrefix("255.255.255.255/32"), // RFC 919 Limited Broadcast
	}

	// IPv6 prefixes whose last 32 bits are an IPv4 address.
	embedsV4 = []netip.Prefix{
		netip.MustParsePrefix("64:ff9b::/96"),    // RFC 6052 NAT64 Well-Known Prefix
		netip.MustParsePrefix("::ffff:0:0:0/96"), // RFC 2765 SIIT IPv4-translated
		netip.MustParsePrefix("::/96"),           // RFC 4291 IPv4-compatible (deprecated)
	}

	prefixULA6    = netip.MustParsePrefix("fc00::/7") // RFC 4193 Unique Local, allowed only with AllowPrivate
	prefixGlobal6 = netip.MustParsePrefix("2000::/3") // RFC 4291 Global Unicast

	// IPv6 special-purpose blocks inside 2000::/3 not globally reachable.
	// Outside 2000::/3 everything is blocked, including 64:ff9b:1::/48
	// (RFC 8215 local-use NAT64), 100::/64 (RFC 6666 discard),
	// 100:0:0:1::/64 (RFC 9780 dummy), 5f00::/16 (RFC 9602 SRv6 SIDs),
	// fec0::/10 (site-local), fe80::/10, and ff00::/8.
	blocked6 = []netip.Prefix{
		netip.MustParsePrefix("2001::/23"),     // RFC 2928 IETF Protocol Assignments: Teredo 2001::/32, benchmarking 2001:2::/48, ORCHID 2001:10::/28, ORCHIDv2 2001:20::/28, ...
		netip.MustParsePrefix("2001:db8::/32"), // RFC 3849 Documentation
		netip.MustParsePrefix("2002::/16"),     // RFC 3056 6to4
		netip.MustParsePrefix("3fff::/20"),     // RFC 9637 Documentation
	}
)
