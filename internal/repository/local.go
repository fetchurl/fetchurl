package repository

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/fetchurl/fetchurl/internal/errutil"
	"github.com/fetchurl/fetchurl/internal/eviction"
)

// LocalRepository implements a Repository backed by the local filesystem.
//
// It uses a directory structure of {cacheDir}/{algo}/{shard}/{hash} to store files.
// Shard is the first two characters of the hash.
type LocalRepository struct {
	CacheDir string
	eviction *eviction.Manager
}

func NewLocalRepository(cacheDir string, eviction *eviction.Manager) *LocalRepository {
	return &LocalRepository{
		CacheDir: cacheDir,
		eviction: eviction,
	}
}

func (r *LocalRepository) getRelPath(algo, hash string) string {
	if len(hash) < 2 {
		return filepath.Join(algo, hash)
	}
	return filepath.Join(algo, hash[:2], hash)
}

func (r *LocalRepository) getPath(algo, hash string) string {
	return filepath.Join(r.CacheDir, r.getRelPath(algo, hash))
}

func (r *LocalRepository) Exists(ctx context.Context, algo, hash string) (bool, error) {
	_, err := os.Stat(r.getPath(algo, hash))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (r *LocalRepository) Get(ctx context.Context, algo, hash string) (io.ReadCloser, int64, error) {
	path := r.getPath(algo, hash)
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		errutil.ReportError(f.Close(), "Failed to close file after stat error", "path", path)
		return nil, 0, err
	}
	if r.eviction != nil {
		r.eviction.Touch(r.getRelPath(algo, hash))
	}
	return f, info.Size(), nil
}

// BeginWrite initiates a write operation for a file.
// It creates a temporary file and returns it along with a commit function.
// The commit function should be called after the file is fully written and verified.
func (r *LocalRepository) BeginWrite(algo, hash string) (io.WriteCloser, func() error, error) {
	finalPath := r.getPath(algo, hash)

	// Create temp file in the same filesystem/dir as final destination (or at least same volume)
	// We can use CacheDir root or a tmp subdir inside it.
	tmpFile, err := os.CreateTemp(r.CacheDir, "put-*")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	committed := false

	commit := func() error {
		if committed {
			return nil
		}
		// Close the file first
		if err := tmpFile.Close(); err != nil {
			return fmt.Errorf("failed to close temp file: %w", err)
		}

		// Ensure destination directory exists
		if err := os.MkdirAll(filepath.Dir(finalPath), 0755); err != nil {
			return fmt.Errorf("failed to create algo/shard dir: %w", err)
		}

		// Move to final path
		if err := os.Rename(tmpFile.Name(), finalPath); err != nil {
			return fmt.Errorf("failed to rename to final path: %w", err)
		}

		committed = true

		// Update eviction
		if r.eviction != nil {
			info, err := os.Stat(finalPath)
			if err != nil {
				errutil.ReportError(err, "Failed to stat committed file", "path", finalPath)
			} else {
				r.eviction.Add(r.getRelPath(algo, hash), info.Size())
				slog.Info("Stored file", "algo", algo, "hash", hash, "size", info.Size())
			}
		}

		return nil
	}

	return tmpFile, commit, nil
}
