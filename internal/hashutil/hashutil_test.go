package hashutil

import (
	"errors"
	"testing"
)

func TestGetHasherUnsupported(t *testing.T) {
	_, err := GetHasher("md5")
	if err == nil {
		t.Fatal("expected error for unsupported algorithm")
	}
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("errors.Is(err, ErrUnsupportedAlgorithm) = false; err = %v", err)
	}
}

func TestIsValidDigest(t *testing.T) {
	emptySHA256 := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	tests := []struct {
		name   string
		algo   string
		digest string
		want   bool
	}{
		{"valid sha256", "sha256", emptySHA256, true},
		{"uppercase hex ok", "SHA-256", "E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855", true},
		{"too short", "sha256", "deadbeef", false},
		{"too long", "sha256", emptySHA256 + "aa", false},
		{"path traversal", "sha256", "../../../etc/passwd" + "00000000000000000000000000000000000000000000", false},
		{"slash in digest", "sha256", "ab/" + emptySHA256[3:], false},
		{"dotdot shard", "sha256", ".." + emptySHA256[2:], false}, // ".." is not hex
		{"unknown algo", "md5", "d41d8cd98f00b204e9800998ecf8427e", false},
		{"sha1 length", "sha1", "da39a3ee5e6b4b0d3255bfef95601890afd80709", true},
		{"sha1 wrong length", "sha1", emptySHA256, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidDigest(tt.algo, tt.digest); got != tt.want {
				t.Fatalf("IsValidDigest(%q, %q) = %v, want %v", tt.algo, tt.digest, got, tt.want)
			}
		})
	}
}
