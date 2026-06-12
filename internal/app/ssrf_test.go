package app

import (
	"os"
	"testing"
)

func TestValidateIP(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{"Valid public IP", "8.8.8.8", false},
		{"Loopback IPv4", "127.0.0.1", true},
		{"Loopback IPv6", "::1", true},
		{"Private IPv4 Class A", "10.0.0.1", true},
		{"Private IPv4 Class B", "172.16.0.1", true},
		{"Private IPv4 Class C", "192.168.0.1", true},
		{"AWS Metadata", "169.254.169.254", true},
		{"Link local IPv6", "fe80::1", true},
		{"Link local IPv6 with zone", "fe80::1%eth0", true},
		{"Unspecified IPv4", "0.0.0.0", true},
		{"Unspecified IPv6", "::", true},
		{"Invalid IP string", "not-an-ip", true},
		{"Empty string", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("FETCHURL_ALLOW_PRIVATE_IPS")
			err := ValidateIP(tt.host)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateIP(%q) error = %v, wantErr %v", tt.host, err, tt.wantErr)
			}
		})
	}
}

func TestValidateIP_AllowPrivate(t *testing.T) {
	os.Setenv("FETCHURL_ALLOW_PRIVATE_IPS", "1")
	defer os.Unsetenv("FETCHURL_ALLOW_PRIVATE_IPS")

	err := ValidateIP("127.0.0.1")
	if err != nil {
		t.Errorf("ValidateIP(\"127.0.0.1\") with FETCHURL_ALLOW_PRIVATE_IPS error = %v, wantErr false", err)
	}
}
