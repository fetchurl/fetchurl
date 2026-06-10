package eviction_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucasew/fetchurl/internal/eviction"
)

func TestManager(t *testing.T) {
	cacheDir := t.TempDir()
	maxBytes := int64(50)
	interval := 10 * time.Millisecond

	strat := eviction.NewLRU()
	policies := []eviction.Policy{&eviction.MaxSizePolicy{MaxBytes: maxBytes}}
	mgr := eviction.NewManager(cacheDir, policies, interval, strat)

	// Create some dummy files
	createFile(t, cacheDir, "file1", 20)
	createFile(t, cacheDir, "file2", 20)
	createFile(t, cacheDir, "file3", 20)

	// Total 60 > 50.
	// We need to tell manager about them (or use LoadInitialState)
	// Let's use LoadInitialState
	if err := mgr.LoadInitialState(); err != nil {
		t.Fatalf("LoadInitialState failed: %v", err)
	}

	// Run Eviction manually
	mgr.RunEviction()

	// Should have evicted 1 file (to get to 40 <= 50)
	// Which one? LoadInitialState reads directory. Order depends on OS.
	// LRU treats them as added in order of ReadDir.
	// So first file read is "oldest" conceptually if we consider Add order.
	// But actually, checking if *any* file was deleted and size is correct.

	remaining, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	if len(remaining) != 2 {
		t.Errorf("Expected 2 files remaining, got %d", len(remaining))
	}

	// Test Add triggering need for eviction (but handled by background loop)
	// We will trigger RunEviction manually for deterministic test.
	createFile(t, cacheDir, "file4", 20)
	mgr.Add("file4", 20)
	// Now 60 again (assuming 2 files left + new one)

	mgr.RunEviction()

	remaining, err = os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(remaining) != 2 {
		t.Errorf("Expected 2 files remaining after second eviction, got %d", len(remaining))
	}
}

func createFile(t *testing.T, dir, name string, size int64) {
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatalf("truncate failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}
