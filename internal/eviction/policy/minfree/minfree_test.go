package minfree

import (
	"math"
	"syscall"
	"testing"
)

func TestFreeBytesPrefersFrsize(t *testing.T) {
	// Bavail=2, Frsize=1024, Bsize=4096 → free must be 2048 (frsize units), not 8192.
	stat := syscall.Statfs_t{Bavail: 2, Frsize: 1024, Bsize: 4096}
	if got := freeBytes(stat); got != 2048 {
		t.Fatalf("freeBytes() = %d, want 2048 (Bavail*Frsize)", got)
	}
}

func TestFreeBytesFallsBackToBsize(t *testing.T) {
	stat := syscall.Statfs_t{Bavail: 3, Frsize: 0, Bsize: 512}
	if got := freeBytes(stat); got != 1536 {
		t.Fatalf("freeBytes() = %d, want 1536 (Bavail*Bsize fallback)", got)
	}

	stat.Frsize = -1
	stat.Bsize = 100
	stat.Bavail = 4
	if got := freeBytes(stat); got != 400 {
		t.Fatalf("freeBytes() negative Frsize = %d, want 400", got)
	}
}

func TestFreeBytesZeroAndSaturate(t *testing.T) {
	if got := freeBytes(syscall.Statfs_t{Bavail: 10, Frsize: 0, Bsize: 0}); got != 0 {
		t.Fatalf("zero units: got %d, want 0", got)
	}
	if got := freeBytes(syscall.Statfs_t{Bavail: 0, Frsize: 4096, Bsize: 4096}); got != 0 {
		t.Fatalf("zero Bavail: got %d, want 0", got)
	}

	// Overflow: Bavail * unit would exceed MaxInt64.
	stat := syscall.Statfs_t{Bavail: math.MaxUint64, Frsize: 4096, Bsize: 4096}
	if got := freeBytes(stat); got != math.MaxInt64 {
		t.Fatalf("overflow: got %d, want MaxInt64", got)
	}
}

func TestPolicyBytesToFreeLiveFS(t *testing.T) {
	dir := t.TempDir()

	// Threshold far below any real free space → no eviction pressure.
	p := &Policy{Path: dir, MinFreeBytes: 1}
	toFree, err := p.BytesToFree(0)
	if err != nil {
		t.Fatalf("BytesToFree: %v", err)
	}
	if toFree != 0 {
		t.Fatalf("toFree = %d, want 0 when disk has free space", toFree)
	}

	// Impossible min-free forces a positive BytesToFree.
	p.MinFreeBytes = math.MaxInt64
	toFree, err = p.BytesToFree(0)
	if err != nil {
		t.Fatalf("BytesToFree max: %v", err)
	}
	if toFree <= 0 {
		t.Fatalf("toFree = %d, want > 0 when MinFreeBytes is MaxInt64", toFree)
	}
}

func TestPolicyStatfsError(t *testing.T) {
	p := &Policy{Path: "/no/such/path/for/minfree/test", MinFreeBytes: 1}
	if _, err := p.BytesToFree(0); err == nil {
		t.Fatal("BytesToFree: want error for missing path")
	}
}
