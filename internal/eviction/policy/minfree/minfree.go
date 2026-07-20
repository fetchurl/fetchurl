package minfree

import (
	"fmt"
	"log/slog"
	"math"
	"syscall"
)

// Policy triggers eviction when disk free space is below a threshold.
type Policy struct {
	Path         string
	MinFreeBytes int64
}

func (m *Policy) BytesToFree(currentSize int64) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(m.Path, &stat); err != nil {
		return 0, fmt.Errorf("failed to check disk space: %w", err)
	}

	freeSpace := freeBytes(stat)

	slog.Debug("Disk space check", "path", m.Path, "free_bytes", freeSpace, "min_required", m.MinFreeBytes)

	if freeSpace < m.MinFreeBytes {
		needed := m.MinFreeBytes - freeSpace
		return needed, nil
	}
	return 0, nil
}

// freeBytes converts Statfs available-block counts into a byte size.
//
// Linux reports f_bavail in units of the fundamental filesystem block size
// (f_frsize). f_bsize is the preferred I/O transfer size and can differ on
// some network and specialized filesystems; using it under- or over-states
// free space and skews min-free-space eviction. Prefer Frsize, fall back to
// Bsize when Frsize is unset, and saturate at MaxInt64 on overflow.
func freeBytes(stat syscall.Statfs_t) int64 {
	unit := stat.Frsize
	if unit <= 0 {
		unit = stat.Bsize
	}
	if unit <= 0 || stat.Bavail == 0 {
		return 0
	}
	u := uint64(unit)
	if stat.Bavail > math.MaxInt64/u {
		return math.MaxInt64
	}
	return int64(stat.Bavail * u)
}
