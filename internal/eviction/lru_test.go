package eviction_test

import (
	"testing"
	"github.com/lucasew/fetchurl/internal/eviction"
)

func TestLRU(t *testing.T) {
	l := eviction.NewLRU()

	l.OnAdd("a", 10)
	l.OnAdd("b", 20)
	l.OnAdd("c", 30)

	// Current order: c, b, a (most recent first)
	// Total size: 60

	// Access a
	l.OnAccess("a")
	// Order: a, c, b

	// Test GetVictims
	// Target 40. Current 60. Need to remove 20.
	// Victims should be from the back: b (20).
	// If we remove b, size becomes 40. Target met.
	victims := l.GetVictims(60, 40)
	if len(victims) != 1 {
		t.Fatalf("expected 1 victim, got %d", len(victims))
	}
	if victims[0].Key != "b" {
		t.Errorf("expected victim b, got %s", victims[0].Key)
	}

	// Test GetVictims with larger reduction
	// Target 10. Current 60.
	// Order: a, c, b
	// Back: b (20) -> size 40
	// Back: c (30) -> size 10
	// Should return b and c.
	victims = l.GetVictims(60, 10)
	if len(victims) != 2 {
		t.Fatalf("expected 2 victims, got %d", len(victims))
	}
	// Order depends on implementation, but likely b then c as we traverse from back
	if victims[0].Key != "b" {
		t.Errorf("expected victim b, got %s", victims[0].Key)
	}
	if victims[1].Key != "c" {
		t.Errorf("expected victim c, got %s", victims[1].Key)
	}
}

func TestLRU_Remove(t *testing.T) {
	l := eviction.NewLRU()
	l.OnAdd("a", 10)
	l.Remove("a")

	victims := l.GetVictims(10, 0)
	if len(victims) != 0 {
		t.Errorf("expected 0 victims after remove, got %d", len(victims))
	}
}
