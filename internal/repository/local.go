package repository

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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

// getPath resolves the on-disk path for algo/hash and rejects any
// resolution that escapes CacheDir (defense in depth against path
// traversal if a caller skips digest validation).
func (r *LocalRepository) getPath(algo, hash string) (string, error) {
	full := filepath.Clean(filepath.Join(r.CacheDir, r.getRelPath(algo, hash)))
	root := filepath.Clean(r.CacheDir)
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("hash path escapes cache directory")
	}
	return full, nil
}

func (r *LocalRepository) Exists(ctx context.Context, algo, hash string) (bool, error) {
	path, err := r.getPath(algo, hash)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (r *LocalRepository) Get(ctx context.Context, algo, hash string) (io.ReadCloser, int64, error) {
	path, err := r.getPath(algo, hash)
	if err != nil {
		return nil, 0, err
	}
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
//
// Commit fsyncs the temp, renames it into the sharded CAS path, then fsyncs the
// destination directory so a crash mid-commit cannot leave a truncated object
// under the final digest path or lose the directory entry for a complete object.
//
// If the write is abandoned, callers must Close the returned writer. Close without
// a successful commit removes the put-* temp so aborted fetches and failed seed
// copies do not leave orphans under the cache root until the next process start.
// Commit also removes the temp if sync/rename/setup fails after the file is closed.
func (r *LocalRepository) BeginWrite(algo, hash string) (io.WriteCloser, func() error, error) {
	finalPath, err := r.getPath(algo, hash)
	if err != nil {
		return nil, nil, err
	}

	// Create temp file in the same filesystem/dir as final destination (or at least same volume)
	// We can use CacheDir root or a tmp subdir inside it.
	tmpFile, err := os.CreateTemp(r.CacheDir, "put-*")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	pw := &pendingWrite{f: tmpFile, name: tmpFile.Name()}

	commit := func() error {
		if pw.committed {
			return nil
		}
		if pw.closed {
			return fmt.Errorf("cannot commit: write session already closed")
		}

		// Flush file data to stable storage before we close and rename. Without
		// this, a crash after rename can leave a truncated CAS object under the
		// final path (kernel page cache not yet written), which later serves as
		// a false cache hit with the wrong bytes.
		if err := pw.f.Sync(); err != nil {
			// Still close+remove so the put-* temp does not linger.
			closeErr := pw.closeFile()
			remErr := pw.removeTemp()
			if closeErr != nil || remErr != nil {
				return fmt.Errorf("failed to sync temp file: %w (close: %v, remove: %v)", err, closeErr, remErr)
			}
			return fmt.Errorf("failed to sync temp file: %w", err)
		}

		// Close the file first (does not delete — we still need the path for rename).
		if err := pw.closeFile(); err != nil {
			pw.removeTemp()
			return fmt.Errorf("failed to close temp file: %w", err)
		}

		// Ensure destination directory exists
		destDir := filepath.Dir(finalPath)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			pw.removeTemp()
			return fmt.Errorf("failed to create algo/shard dir: %w", err)
		}

		// Move to final path
		if err := os.Rename(pw.name, finalPath); err != nil {
			pw.removeTemp()
			return fmt.Errorf("failed to rename to final path: %w", err)
		}

		// Durably record the new directory entry. File Sync above makes object
		// bytes stable; without a directory fsync, a power loss can still drop
		// the rename so the CAS path is missing after reboot (spec: addition
		// MUST be atomic). The object already lives at finalPath — if dir sync
		// fails we keep it and report rather than delete a complete object.
		if err := fsyncDir(destDir); err != nil {
			errutil.ReportError(err, "Failed to fsync CAS directory after rename", "dir", destDir, "path", finalPath)
		}

		pw.committed = true

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

	return pw, commit, nil
}

// fsyncDir flushes directory metadata (e.g. the rename that installed a CAS
// object) to stable storage.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// pendingWrite is an in-progress CAS object write. Closing without a successful
// commit deletes the temp file (abort path).
type pendingWrite struct {
	f         *os.File
	name      string
	committed bool
	closed    bool
}

func (p *pendingWrite) Write(b []byte) (int, error) {
	if p.closed {
		return 0, fmt.Errorf("write to closed pending write")
	}
	return p.f.Write(b)
}

// Close closes the temp file and, if commit never succeeded, removes it.
func (p *pendingWrite) Close() error {
	if p.closed {
		return nil
	}
	err := p.closeFile()
	if !p.committed {
		if remErr := p.removeTemp(); remErr != nil {
			if err != nil {
				return fmt.Errorf("close temp: %w; remove temp: %v", err, remErr)
			}
			return remErr
		}
	}
	return err
}

func (p *pendingWrite) closeFile() error {
	p.closed = true
	return p.f.Close()
}

func (p *pendingWrite) removeTemp() error {
	if err := os.Remove(p.name); err != nil && !os.IsNotExist(err) {
		errutil.ReportError(err, "Failed to remove write temp", "path", p.name)
		return err
	}
	return nil
}
