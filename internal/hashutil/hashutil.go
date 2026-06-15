package hashutil

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

// emptyHash holds the lowercase hex digest of the zero-length input
// for each supported algorithm. Used for the spec SHOULD fast-path.
var emptyHash = map[string]string{
	"sha1":   "da39a3ee5e6b4b0d3255bfef95601890afd80709",
	"sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	"sha512": "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
}

// IsEmptyHash reports whether the supplied algo+hash identifies the
// content hash of the empty byte slice. Per spec, servers SHOULD be
// able to satisfy these without contacting any upstream or source.
func IsEmptyHash(algo, hash string) bool {
	norm := NormalizeAlgo(algo)
	want, ok := emptyHash[norm]
	if !ok {
		return false
	}
	// Accept either case; spec requires lowercase representation but callers may vary.
	return strings.EqualFold(want, hash)
}
