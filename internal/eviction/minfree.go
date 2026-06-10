package eviction

import (
	"fmt"
	"log/slog"
	"syscall"
)

// Policy triggers eviction when disk free space is below a threshold.
type MinFreePolicy struct {
	Path         string
	MinFreeBytes int64
}

func (m *MinFreePolicy) BytesToFree(currentSize int64) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(m.Path, &stat); err != nil {
		return 0, fmt.Errorf("failed to check disk space: %w", err)
	}

	// Available blocks * block size
	freeSpace := int64(stat.Bavail) * int64(stat.Bsize)

	slog.Debug("Disk space check", "path", m.Path, "free_bytes", freeSpace, "min_required", m.MinFreeBytes)

	if freeSpace < m.MinFreeBytes {
		needed := m.MinFreeBytes - freeSpace
		return needed, nil
	}
	return 0, nil
}
