package eviction_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fetchurl/fetchurl/internal/eviction"
	"github.com/fetchurl/fetchurl/internal/eviction/lru"
	"github.com/fetchurl/fetchurl/internal/eviction/policy"
	"github.com/fetchurl/fetchurl/internal/eviction/policy/maxsize"
)

func TestManager(t *testing.T) {
	cacheDir := t.TempDir()
	maxBytes := int64(50)
	interval := 10 * time.Millisecond

	strat := lru.New()
	policies := []policy.Policy{&maxsize.Policy{MaxBytes: maxBytes}}
	mgr := eviction.NewManager(cacheDir, policies, interval, strat)

	// CAS layout: {algo}/{shard}/{hash}
	file1 := filepath.Join("sha256", "a1", "a1file1")
	file2 := filepath.Join("sha256", "a2", "a2file2")
	file3 := filepath.Join("sha256", "a3", "a3file3")
	createFile(t, cacheDir, file1, 20)
	createFile(t, cacheDir, file2, 20)
	createFile(t, cacheDir, file3, 20)

	// Total 60 > 50.
	if err := mgr.LoadInitialState(); err != nil {
		t.Fatalf("LoadInitialState failed: %v", err)
	}

	mgr.RunEviction()

	// Should have evicted 1 file (to get to 40 <= 50).
	remaining := countCASFiles(t, cacheDir)
	if remaining != 2 {
		t.Errorf("Expected 2 CAS files remaining, got %d", remaining)
	}

	file4 := filepath.Join("sha256", "a4", "a4file4")
	createFile(t, cacheDir, file4, 20)
	mgr.Add(file4, 20)

	mgr.RunEviction()

	remaining = countCASFiles(t, cacheDir)
	if remaining != 2 {
		t.Errorf("Expected 2 CAS files remaining after second eviction, got %d", remaining)
	}
}

func countCASFiles(t *testing.T, cacheDir string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(cacheDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(cacheDir, path)
		if err != nil {
			return err
		}
		if strings.Count(rel, string(os.PathSeparator)) == 2 {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir failed: %v", err)
	}
	return count
}

func createFile(t *testing.T, dir, name string, size int64) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
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

// A walk error must not leave the strategy half-populated while currentBytes
// stays zero — that under-counts cache size and skips eviction.
func TestLoadInitialStateWalkErrorDoesNotMutateStrategy(t *testing.T) {
	cacheDir := t.TempDir()
	// Real CAS object visited before the unreadable dir (lexical order).
	casRel := filepath.Join("sha256", "aa", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	createFile(t, cacheDir, casRel, 10)

	// Unreadable subdirectory makes WalkDir fail after the CAS path is seen.
	// Without deferred apply, OnAdd would have already mutated the strategy.
	badDir := filepath.Join(cacheDir, "zzz-unreadable")
	if err := os.Mkdir(badDir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "x"), []byte("y"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(badDir, 0); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(badDir, 0o755); err != nil {
			t.Errorf("restore Chmod: %v", err)
		}
	})

	strat := lru.New()
	mgr := eviction.NewManager(cacheDir, nil, time.Minute, strat)
	if err := mgr.LoadInitialState(); err == nil {
		t.Fatal("LoadInitialState: want error when a subdirectory is unreadable")
	}
	if got := mgr.CurrentBytes(); got != 0 {
		t.Errorf("CurrentBytes = %d, want 0 after failed load", got)
	}
	// No CAS entries applied despite walk having seen casRel before the error.
	victims := strat.GetVictims(10, 0)
	if len(victims) != 0 {
		t.Errorf("strategy victims = %+v, want empty after failed load", victims)
	}
}

// Orphan put-*/seed-* temps at the cache root must not inflate accounting or
// survive LoadInitialState — otherwise a crash mid-write makes the next boot
// over-count cache size and evict real CAS objects early.
func TestLoadInitialStateSkipsAndCleansOrphanTemps(t *testing.T) {
	cacheDir := t.TempDir()
	// Real CAS object under {algo}/{shard}/{hash}.
	casRel := filepath.Join("sha256", "e3", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	createFile(t, cacheDir, casRel, 10)
	createFile(t, cacheDir, "put-orphan123", 100)
	createFile(t, cacheDir, "seed-orphan456", 50)
	// Stray non-temp debris: ignore for size, do not delete.
	createFile(t, cacheDir, "README", 5)

	strat := lru.New()
	mgr := eviction.NewManager(cacheDir, nil, time.Minute, strat)
	if err := mgr.LoadInitialState(); err != nil {
		t.Fatalf("LoadInitialState failed: %v", err)
	}

	if got := mgr.CurrentBytes(); got != 10 {
		t.Errorf("CurrentBytes = %d, want 10 (CAS only)", got)
	}

	for _, name := range []string{"put-orphan123", "seed-orphan456"} {
		if _, err := os.Stat(filepath.Join(cacheDir, name)); !os.IsNotExist(err) {
			t.Errorf("orphan temp %s still present (err=%v)", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "README")); err != nil {
		t.Errorf("non-temp stray file should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, casRel)); err != nil {
		t.Errorf("CAS object missing after load: %v", err)
	}

	// Strategy should only know about the CAS key (evicting would target it alone).
	victims := strat.GetVictims(10, 0)
	if len(victims) != 1 || victims[0].Key != casRel || victims[0].Size != 10 {
		t.Errorf("strategy victims = %+v, want single CAS entry", victims)
	}
}
