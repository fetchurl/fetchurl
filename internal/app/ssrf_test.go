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
		{"Loopback IPv4", "127.0.0.1", false, true},
		{"Loopback IPv6", "::1", false, true},
		{"Private IPv4 Class A", "10.0.0.1", false, true},
		{"Private IPv4 Class B", "172.16.0.1", false, true},
		{"Private IPv4 Class C", "192.168.0.1", false, true},
		{"AWS Metadata", "169.254.169.254", false, true},
		{"Link local IPv6", "fe80::1", false, true},
		{"Link local IPv6 with zone", "fe80::1%eth0", false, true},
		{"Unspecified IPv4", "0.0.0.0", false, true},
		{"Unspecified IPv6", "::", false, true},
		{"Invalid IP string", "not-an-ip", false, true},
		{"Empty string", "", false, true},
		{"Allow Private - Loopback", "127.0.0.1", true, false},
		{"Allow Private - Private IP", "10.0.0.1", true, false},
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
