package eviction

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fetchurl/fetchurl/internal/errutil"
	"github.com/fetchurl/fetchurl/internal/eviction/policy"
)

// Manager manages cache eviction by coordinating between storage usage,
// configured policies (e.g., max size, min free space), and an eviction strategy (e.g., LRU).
//
// It runs a background loop to periodically enforce these policies.
type Manager struct {
	cacheDir     string
	policies     []policy.Policy
	strategy     Strategy
	currentBytes atomic.Int64
	interval     time.Duration
}

// NewManager creates a new Manager instance.
//
// It does not automatically start the eviction loop; call Start() to begin background processing.
func NewManager(cacheDir string, policies []policy.Policy, interval time.Duration, strategy Strategy) *Manager {
	return &Manager{
		cacheDir: cacheDir,
		policies: policies,
		interval: interval,
		strategy: strategy,
	}
}

// LoadInitialState scans the cache directory to rebuild the in-memory strategy state.
//
// This method walks the entire cache directory to calculate current usage and
// populate the eviction strategy (e.g., LRU list).
//
// Only committed CAS objects under {algo}/{shard}/{hash} are counted. Orphan
// write temps (put-* from BeginWrite, seed-* from seed) left at the cache root
// after a crash would otherwise inflate currentBytes and cause premature
// eviction of real objects; those known temps are removed best-effort.
//
// Note: This operation can be I/O intensive for large caches and should be called
// before starting the server or the eviction loop.
func (m *Manager) LoadInitialState() error {
	var totalSize int64
	var count int

	err := filepath.WalkDir(m.cacheDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == m.cacheDir {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(m.cacheDir, path)
		if err != nil {
			errutil.LogMsg(err, "Failed to get relative path", "path", path)
			return nil
		}

		// Drop orphan write temps at the cache root (CreateTemp put-*/seed-*).
		// Do not count them toward size — they are not CAS objects.
		if isOrphanWriteTemp(rel) {
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				errutil.LogMsg(removeErr, "Failed to remove orphan cache temp", "path", path)
			} else {
				slog.Info("Removed orphan cache temp", "path", rel)
			}
			return nil
		}

		// CAS layout is {algo}/{shard}/{hash}. Anything else (stray files,
		// incomplete layouts) is ignored so it cannot skew capacity.
		if !isCASObjectRel(rel) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			errutil.LogMsg(err, "Failed to get file info", "file", path)
			return nil
		}

		size := info.Size()
		totalSize += size
		count++
		m.strategy.OnAdd(rel, size)
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk cache dir: %w", err)
	}

	m.currentBytes.Store(totalSize)
	slog.Info("Initial cache state loaded", "count", count, "size", totalSize)
	return nil
}

// isCASObjectRel reports whether rel is a committed CAS object path:
// {algo}/{shard}/{hash} (exactly two path separators).
func isCASObjectRel(rel string) bool {
	if rel == "" || rel == "." {
		return false
	}
	// filepath.Rel uses OS separators; refuse anything that escapes via "..".
	if strings.Contains(rel, "..") {
		return false
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	return len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != ""
}

// isOrphanWriteTemp reports whether rel is a top-level CreateTemp name used for
// in-progress CAS writes (repository put-*, seed seed-*).
func isOrphanWriteTemp(rel string) bool {
	if strings.Contains(rel, string(os.PathSeparator)) {
		return false
	}
	base := filepath.Base(rel)
	return strings.HasPrefix(base, "put-") || strings.HasPrefix(base, "seed-")
}

// Start runs the background eviction loop.
//
// It blocks until the context is canceled. It should typically be run in a separate goroutine.
// The loop triggers RunEviction() at the configured interval.
func (m *Manager) Start(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.RunEviction()
		}
	}
}

// Add registers a new item with the eviction strategy and updates the total cache size.
//
// It should be called whenever a new item is successfully committed to the cache.
func (m *Manager) Add(key string, size int64) {
	diff := m.strategy.OnAdd(key, size)
	m.currentBytes.Add(diff)
}

// Touch notifies the strategy that an item has been accessed.
//
// For strategies like LRU, this promotes the item to prevent it from being evicted.
func (m *Manager) Touch(key string) {
	m.strategy.OnAccess(key)
}

// CurrentBytes returns the tracked total size of committed CAS objects.
func (m *Manager) CurrentBytes() int64 {
	return m.currentBytes.Load()
}

// RunEviction enforces eviction policies by removing files if thresholds are exceeded.
//
// The process is:
// 1. Check all policies to determine how many bytes need to be freed.
// 2. If space needs to be freed, query the strategy for victim files.
// 3. Delete the victim files from disk.
// 4. Update the strategy and total size to reflect the deletions.
func (m *Manager) RunEviction() {
	current := m.currentBytes.Load()
	var maxToFree int64

	for _, p := range m.policies {
		toFree, err := p.BytesToFree(current)
		if err != nil {
			errutil.ReportError(err, "Failed to check capacity policy")
			continue
		}
		if toFree > maxToFree {
			maxToFree = toFree
		}
	}

	if maxToFree <= 0 {
		return
	}

	targetSize := current - maxToFree
	// Ensure target is not negative (though Strategy logic should handle it)
	if targetSize < 0 {
		targetSize = 0
	}

	victims := m.strategy.GetVictims(current, targetSize)
	if len(victims) == 0 {
		return
	}

	slog.Info("Evicting files", "count", len(victims), "current_size", current, "to_free", maxToFree, "target", targetSize)

	for _, victim := range victims {
		path := filepath.Join(m.cacheDir, victim.Key)
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			errutil.ReportError(err, "Failed to remove file", "path", path)
			// Continue to next victim?
			// If we can't remove, we shouldn't decrement size?
			// But we remove from strategy to avoid loop.
		}

		m.strategy.Remove(victim.Key)

		// If remove succeeded (or file didn't exist), we consider it gone.
		if err == nil || os.IsNotExist(err) {
			m.currentBytes.Add(-victim.Size)
		}
	}
}
