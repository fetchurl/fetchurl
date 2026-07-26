package app

import (
	"errors"
	"testing"
)

func TestValidateIP(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		allowPrivate bool
		wantErr      error
	}{
		{"Valid public IP", "8.8.8.8", false, nil},
		{"Valid public IPv6", "2001:4860:4860::8888", false, nil},
		{"Loopback IPv4", "127.0.0.1", false, ErrBlockedInternalIP},
		{"Loopback IPv6", "::1", false, ErrBlockedInternalIP},
		{"Private IPv4 Class A", "10.0.0.1", false, ErrBlockedInternalIP},
		{"Private IPv4 Class B", "172.16.0.1", false, ErrBlockedInternalIP},
		{"Private IPv4 Class C", "192.168.0.1", false, ErrBlockedInternalIP},
		{"Private IPv6 ULA", "fd12:3456:789a::1", false, ErrBlockedInternalIP},
		{"AWS Metadata", "169.254.169.254", false, ErrBlockedInternalIP},
		{"Link local IPv6", "fe80::1", false, ErrBlockedInternalIP},
		{"Link local IPv6 with zone", "fe80::1%eth0", false, ErrBlockedInternalIP},
		{"Unspecified IPv4", "0.0.0.0", false, ErrBlockedInternalIP},
		{"Unspecified IPv6", "::", false, ErrBlockedInternalIP},
		// RFC 1122 "this network" 0.0.0.0/8 — IsUnspecified only covers 0.0.0.0.
		{"This network low", "0.0.0.1", false, ErrBlockedInternalIP},
		{"This network high", "0.255.255.255", false, ErrBlockedInternalIP},
		{"IPv4-mapped this network", "::ffff:0.0.0.1", false, ErrBlockedInternalIP},
		// RFC 6598 CGNAT / shared address space — not IsPrivate, still internal.
		{"CGNAT low", "100.64.0.1", false, ErrBlockedInternalIP},
		{"CGNAT high", "100.127.255.254", false, ErrBlockedInternalIP},
		{"Just below CGNAT", "100.63.255.255", false, nil},
		{"Just above CGNAT", "100.128.0.1", false, nil},
		{"IPv4-mapped CGNAT", "::ffff:100.64.0.1", false, ErrBlockedInternalIP},
		{"IPv4-mapped loopback", "::ffff:127.0.0.1", false, ErrBlockedInternalIP},
		{"IPv4 multicast", "224.0.0.1", false, ErrBlockedInternalIP},
		{"IPv6 multicast", "ff02::1", false, ErrBlockedInternalIP},
		{"Invalid IP string", "not-an-ip", false, ErrInvalidIP},
		{"Empty string", "", false, ErrInvalidIP},
		{"Allow Private - Loopback", "127.0.0.1", true, nil},
		{"Allow Private - Private IP", "10.0.0.1", true, nil},
		{"Allow Private - CGNAT", "100.64.0.1", true, nil},
		{"Allow Private - This network", "0.0.0.1", true, nil},
		{"Allow Private - Multicast", "224.0.0.1", true, nil},
		{"Allow Private - Still fails invalid", "not-an-ip", true, ErrInvalidIP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIP(tt.host, tt.allowPrivate)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("ValidateIP(%q, %v) error = %v, want nil", tt.host, tt.allowPrivate, err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateIP(%q, %v) error = %v, want errors.Is(..., %v)", tt.host, tt.allowPrivate, err, tt.wantErr)
			}
		})
	}
}
