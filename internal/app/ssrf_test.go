package app

import (
	"testing"
)

func TestValidateIP(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		allowPrivate bool
		wantErr      bool
	}{
		{"Valid public IP", "8.8.8.8", false, false},
		{"Valid public IPv6", "2001:4860:4860::8888", false, false},
		{"Loopback IPv4", "127.0.0.1", false, true},
		{"Loopback IPv6", "::1", false, true},
		{"Private IPv4 Class A", "10.0.0.1", false, true},
		{"Private IPv4 Class B", "172.16.0.1", false, true},
		{"Private IPv4 Class C", "192.168.0.1", false, true},
		{"Private IPv6 ULA", "fd12:3456:789a::1", false, true},
		{"AWS Metadata", "169.254.169.254", false, true},
		{"Link local IPv6", "fe80::1", false, true},
		{"Link local IPv6 with zone", "fe80::1%eth0", false, true},
		{"Unspecified IPv4", "0.0.0.0", false, true},
		{"Unspecified IPv6", "::", false, true},
		// RFC 6598 CGNAT / shared address space — not IsPrivate, still internal.
		{"CGNAT low", "100.64.0.1", false, true},
		{"CGNAT high", "100.127.255.254", false, true},
		{"Just below CGNAT", "100.63.255.255", false, false},
		{"Just above CGNAT", "100.128.0.1", false, false},
		{"IPv4-mapped CGNAT", "::ffff:100.64.0.1", false, true},
		{"IPv4-mapped loopback", "::ffff:127.0.0.1", false, true},
		{"IPv4 multicast", "224.0.0.1", false, true},
		{"IPv6 multicast", "ff02::1", false, true},
		{"Invalid IP string", "not-an-ip", false, true},
		{"Empty string", "", false, true},
		{"Allow Private - Loopback", "127.0.0.1", true, false},
		{"Allow Private - Private IP", "10.0.0.1", true, false},
		{"Allow Private - CGNAT", "100.64.0.1", true, false},
		{"Allow Private - Multicast", "224.0.0.1", true, false},
		{"Allow Private - Still fails invalid", "not-an-ip", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIP(tt.host, tt.allowPrivate)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateIP(%q, %v) error = %v, wantErr %v", tt.host, tt.allowPrivate, err, tt.wantErr)
			}
		})
	}
}
