package eviction_test

import (
	"errors"
	"testing"

	"github.com/fetchurl/fetchurl/internal/eviction"
	_ "github.com/fetchurl/fetchurl/internal/eviction/lru"
)

func TestGetStrategyNotFound(t *testing.T) {
	_, err := eviction.GetStrategy("no-such-strategy")
	if err == nil {
		t.Fatal("expected error for unknown strategy")
	}
	if !errors.Is(err, eviction.ErrStrategyNotFound) {
		t.Fatalf("errors.Is(err, ErrStrategyNotFound) = false; err = %v", err)
	}
	// Stable message prefix for logs/CLI (name still appended).
	if got, want := err.Error(), "strategy not found: no-such-strategy"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestGetStrategyKnown(t *testing.T) {
	s, err := eviction.GetStrategy("lru")
	if err != nil {
		t.Fatalf("GetStrategy(lru): %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil strategy")
	}
}
