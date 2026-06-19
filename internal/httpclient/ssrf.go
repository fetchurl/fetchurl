package httpclient

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
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("SSRF prevention: blocked access to internal IP %s", ip)
		}
		// Block AWS metadata IP explicitly just in case
		if ip.Equal(net.ParseIP("169.254.169.254")) {
			return fmt.Errorf("SSRF prevention: blocked access to metadata IP %s", ip)
		}
	}
	return nil
}
