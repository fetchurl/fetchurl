package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/fetchurl/fetchurl/internal/errutil"
	"github.com/fetchurl/fetchurl/internal/eviction"
)

// Domain errors for the on-disk CAS layout. Callers can errors.Is these;
// they carry no extra wrap cause of their own.
var (
	ErrPathEscapesCache = errors.New("hash path escapes cache directory")
	ErrNotRegularFile   = errors.New("CAS path is not a regular file")
	ErrCommitClosed     = errors.New("cannot commit: write session already closed")
	ErrWriteClosed      = errors.New("write to closed pending write")
)

// LocalRepository implements a Repository backed by the local filesystem.
//
// It uses a directory structure of {cacheDir}/{algo}/{shard}/{hash} to store files.
// Shard is the first two characters of the hash.
//
// Object I/O is scoped with os.Root so path components and symlinks cannot
// escape CacheDir (string Clean+prefix checks alone still follow a symlink
// planted under the cache tree to an arbitrary host path).
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

// casRel returns the CAS object path relative to CacheDir, rejecting names
// that Clean to ".." or otherwise escape the root as a string. os.Root is
// still used at open time so symlinks cannot complete an escape.
func (r *LocalRepository) casRel(algo, hash string) (string, error) {
	rel := filepath.Clean(r.getRelPath(algo, hash))
	if rel == "" || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(os.PathSeparator)) ||
		filepath.IsAbs(rel) {
		return "", ErrPathEscapesCache
	}
	return rel, nil
}

func (r *LocalRepository) openRoot() (*os.Root, error) {
	root, err := os.OpenRoot(r.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("open cache root: %w", err)
	}
	return root, nil
}

func (r *LocalRepository) Exists(ctx context.Context, algo, hash string) (bool, error) {
	rel, err := r.casRel(algo, hash)
	if err != nil {
		return false, err
	}
	root, err := r.openRoot()
	if err != nil {
		return false, err
	}
	defer func() {
		errutil.LogMsg(root.Close(), "Failed to close cache root")
	}()

	// Lstat: do not follow a symlink that could point outside CacheDir.
	info, err := root.Lstat(rel)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() {
		// Symlink/dir/device under a digest path is not a CAS object.
		return false, ErrNotRegularFile
	}
	return true, nil
}

func (r *LocalRepository) Get(ctx context.Context, algo, hash string) (io.ReadCloser, int64, error) {
	rel, err := r.casRel(algo, hash)
	if err != nil {
		return nil, 0, err
	}
	root, err := r.openRoot()
	if err != nil {
		return nil, 0, err
	}
	// Close root after Open: the returned *os.File stays valid; Root only
	// scopes path resolution. Defer close once we have finished opening.
	var rootClosed bool
	closeRoot := func() {
		if !rootClosed {
			rootClosed = true
			errutil.LogMsg(root.Close(), "Failed to close cache root")
		}
	}
	defer closeRoot()

	info, err := root.Lstat(rel)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, ErrNotRegularFile
	}

	f, err := root.Open(rel)
	if err != nil {
		return nil, 0, err
	}
	// Re-check after open: TOCTOU against a symlink swap between Lstat and Open.
	fi, err := f.Stat()
	if err != nil {
		errutil.ReportError(f.Close(), "Failed to close file after stat error", "path", rel)
		return nil, 0, err
	}
	if !fi.Mode().IsRegular() {
		errutil.ReportError(f.Close(), "Failed to close non-regular CAS file", "path", rel)
		return nil, 0, ErrNotRegularFile
	}

	if r.eviction != nil {
		r.eviction.Touch(rel)
	}
	return f, fi.Size(), nil
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
	rel, err := r.casRel(algo, hash)
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
	tmpBase := filepath.Base(tmpFile.Name())

	commit := func() error {
		if pw.committed {
			return nil
		}
		if pw.closed {
			return ErrCommitClosed
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

		root, err := r.openRoot()
		if err != nil {
			pw.removeTemp()
			return err
		}
		defer func() {
			errutil.LogMsg(root.Close(), "Failed to close cache root")
		}()

		// Ensure destination directory exists (scoped to cache root).
		destDir := filepath.Dir(rel)
		if destDir != "." && destDir != "" {
			if err := root.MkdirAll(destDir, 0755); err != nil {
				pw.removeTemp()
				return fmt.Errorf("failed to create algo/shard dir: %w", err)
			}
		}

		// Move to final path within the root so a symlink at the destination
		// cannot redirect the install outside CacheDir.
		if err := root.Rename(tmpBase, rel); err != nil {
			pw.removeTemp()
			return fmt.Errorf("failed to rename to final path: %w", err)
		}

		// Durably record the new directory entry. File Sync above makes object
		// bytes stable; without a directory fsync, a power loss can still drop
		// the rename so the CAS path is missing after reboot (spec: addition
		// MUST be atomic). The object already lives at rel — if dir sync
		// fails we keep it and report rather than delete a complete object.
		if err := fsyncRootDir(root, destDir); err != nil {
			errutil.ReportError(err, "Failed to fsync CAS directory after rename", "dir", destDir, "path", rel)
		}

		pw.committed = true

		// Update eviction
		if r.eviction != nil {
			info, err := root.Stat(rel)
			if err != nil {
				errutil.ReportError(err, "Failed to stat committed file", "path", rel)
			} else {
				r.eviction.Add(rel, info.Size())
				slog.Info("Stored file", "algo", algo, "hash", hash, "size", info.Size())
			}
		}

		return nil
	}

	return pw, commit, nil
}

// fsyncRootDir flushes directory metadata for a path relative to root.
func fsyncRootDir(root *os.Root, dir string) error {
	if dir == "" || dir == "." {
		dir = "."
	}
	d, err := root.Open(dir)
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
		return 0, ErrWriteClosed
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
