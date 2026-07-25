package app

import (
	"fmt"
	"net"
)

func ValidateIP(host string, allowPrivate bool) error {
	// Remove IPv6 zone index if present before parsing
	// e.g. fe80::1%eth0 -> fe80::1
	if zoneIdx := len(host) - 1; zoneIdx >= 0 {
		for i := len(host) - 1; i >= 0; i-- {
			if host[i] == '%' {
				host = host[:i]
				break
			}
		}
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// Prevent bypass using malformed IP strings that get resolved weirdly downstream
		return fmt.Errorf("SSRF prevention: could not parse IP address %s", host)
	}

	// We skip SSRF checks if explicitly allowed.
	// This is necessary for testcontainers-based integration tests.
	if !allowPrivate {
		// RFC 1918 / ULA private, loopback, unspecified, link-local (includes
		// 169.254.169.254 cloud metadata), multicast, RFC 6598 shared address
		// space (CGNAT 100.64.0.0/10), and RFC 1122 "this network" 0.0.0.0/8.
		// net.IP.IsUnspecified only matches 0.0.0.0 itself; the rest of /8 is
		// still non-globally-routable and must not be dialable as an origin.
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
			ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsMulticast() || isSharedAddressSpace(ip) || isThisNetwork(ip) {
			return fmt.Errorf("SSRF prevention: blocked access to internal IP %s", ip)
		}
	}
	return nil
}

// isSharedAddressSpace reports whether ip is in RFC 6598 Carrier-Grade NAT
// space 100.64.0.0/10 (including IPv4-mapped IPv6 forms). net.IP.IsPrivate
// does not cover this range, but it is not globally routable and is commonly
// used for internal infrastructure.
func isSharedAddressSpace(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	// 100.64.0.0/10 → second octet 64–127
	return ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

// isThisNetwork reports whether ip is in RFC 1122 "this network" 0.0.0.0/8
// (including IPv4-mapped IPv6 forms). Only 0.0.0.0 is IsUnspecified; 0.0.0.1
// and siblings are IsGlobalUnicast in Go but are not public Internet addresses.
func isThisNetwork(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] == 0
}
