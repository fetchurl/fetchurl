package repository

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalRepository(t *testing.T) {
	cacheDir := t.TempDir()
	repo := NewLocalRepository(cacheDir, nil)
	ctx := context.Background()
	algo := "sha256"
	hash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // Empty string hash
	content := ""

	t.Run("BeginWrite and Commit", func(t *testing.T) {
		w, commit, err := repo.BeginWrite(algo, hash)
		if err != nil {
			t.Fatalf("BeginWrite failed: %v", err)
		}

		// Write content
		_, err = io.Copy(w, strings.NewReader(content))
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}

		// Commit
		err = commit()
		if err != nil {
			t.Fatalf("Commit failed: %v", err)
		}

		// Verify file exists in sharded path
		shard := hash[:2]
		expectedPath := filepath.Join(cacheDir, algo, shard, hash)
		if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
			t.Errorf("File not found at %s", expectedPath)
		}
	})

	t.Run("Get Success", func(t *testing.T) {
		rc, size, err := repo.Get(ctx, algo, hash)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		defer func() {
			if err := rc.Close(); err != nil {
				t.Errorf("failed to close rc: %v", err)
			}
		}()

		if size != int64(len(content)) {
			t.Errorf("Expected size %d, got %d", len(content), size)
		}

		bytes, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll failed: %v", err)
		}
		if string(bytes) != content {
			t.Errorf("Expected content %q, got %q", content, string(bytes))
		}
	})

	t.Run("Exists Success", func(t *testing.T) {
		exists, err := repo.Exists(ctx, algo, hash)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if !exists {
			t.Error("Exists returned false")
		}
	})

	t.Run("Exists Fail", func(t *testing.T) {
		exists, err := repo.Exists(ctx, algo, "badhash")
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if exists {
			t.Error("Exists returned true for bad hash")
		}
	})

	t.Run("Commit without Close", func(t *testing.T) {
		// Test that commit closes the writer if not closed
		hash2 := "deadbeef"
		w, commit, err := repo.BeginWrite(algo, hash2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(w, "test"); err != nil {
			t.Fatalf("Fprintf failed: %v", err)
		}
		// Not calling w.Close()
		err = commit()
		if err != nil {
			t.Fatalf("Commit failed when not closed: %v", err)
		}
		// Verify content
		rc, _, err := repo.Get(ctx, algo, hash2)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		defer func() {
			if err := rc.Close(); err != nil {
				t.Errorf("failed to close rc: %v", err)
			}
		}()
		bytes, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll failed: %v", err)
		}
		if string(bytes) != "test" {
			t.Errorf("Content mismatch")
		}
	})

	t.Run("Path traversal hash rejected", func(t *testing.T) {
		// hash ".." → Join(cache, algo, "..", "..") cleans to the parent of CacheDir.
		evil := ".."
		if _, _, err := repo.BeginWrite(algo, evil); err == nil {
			t.Fatal("BeginWrite accepted path-escaping hash")
		}
		if _, err := repo.Exists(ctx, algo, evil); err == nil {
			t.Fatal("Exists accepted path-escaping hash")
		}
		if _, _, err := repo.Get(ctx, algo, evil); err == nil {
			t.Fatal("Get accepted path-escaping hash")
		}
	})
}
