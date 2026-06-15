package util

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"slices"
	"strings"
)

type HashFactory func() hash.Hash

var registry = map[string]HashFactory{
	"sha1":   sha1.New,
	"sha256": sha256.New,
	"sha512": sha512.New,
}

func Register(name string, factory HashFactory) {
	registry[name] = factory
}

// NormalizeAlgo lowercases the algorithm name and strips any character
// that is not in [a-z0-9], so that e.g. "SHA256", "SHA-256", "sha-256"
// all resolve to "sha256".
func NormalizeAlgo(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		default:
			return -1 // drop
		}
	}, name)
}

func GetHasher(name string) (hash.Hash, error) {
	factory, ok := registry[NormalizeAlgo(name)]
	if !ok {
		return nil, fmt.Errorf("unsupported hash algorithm: %s", name)
	}
	return factory(), nil
}

func IsSupported(name string) bool {
	_, ok := registry[NormalizeAlgo(name)]
	return ok
}

func SupportedAlgorithms() []string {
	algorithms := make([]string, 0, len(registry))
	for name := range registry {
		algorithms = append(algorithms, name)
	}
	slices.Sort(algorithms)
	return algorithms
}
