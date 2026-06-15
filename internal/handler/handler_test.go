package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasew/fetchurl/internal/repository"
)

func TestCASHandler(t *testing.T) {
	// Setup temporary cache dir
	cacheDir := t.TempDir()
	localRepo := repository.NewLocalRepository(cacheDir, nil)
	// We use the default client for the handler
	h := NewCASHandler(localRepo, nil, nil, t.Context())

	// Setup mock upstream server (origin server for files)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file1":
			if _, err := w.Write([]byte("content1")); err != nil {
				t.Fatalf("failed to write content1: %v", err)
			}
		case "/file2":
			if _, err := w.Write([]byte("content2")); err != nil {
				t.Fatalf("failed to write content2: %v", err)
			}
		case "/fail":
			w.WriteHeader(http.StatusInternalServerError)
		case "/big":
			w.Header().Set("Content-Length", "10")
			if _, err := w.Write([]byte("0123456789")); err != nil {
				t.Fatalf("failed to write big content: %v", err)
			}
		case "/no-len":
			// Force chunked encoding to simulate missing Content-Length
			w.Header().Set("Transfer-Encoding", "chunked")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("content")); err != nil {
				t.Fatalf("failed to write no-len content: %v", err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer origin.Close()

	// Calculate hashes
	hash1 := sha256Sum([]byte("content1"))
	hash2 := sha256Sum([]byte("content2"))

	t.Run("Download Success", func(t *testing.T) {
		req := httptest.NewRequest("GET", fmt.Sprintf("/sha256/%s", hash1), nil)
		req.Header.Set("X-Source-Urls", "\""+origin.URL+"/file1\"")
		w := httptest.NewRecorder()

		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
		}
		if w.Body.String() != "content1" {
			t.Errorf("expected body content1, got %s", w.Body.String())
		}

		// Verify headers (MUST Content-Type + SHOULD Cache-Control on responses)
		if w.Header().Get("Content-Type") != "application/octet-stream" {
			t.Errorf("expected Content-Type application/octet-stream, got %s", w.Header().Get("Content-Type"))
		}
		if w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Errorf("expected Cache-Control header, got %s", w.Header().Get("Cache-Control"))
		}
		if w.Header().Get("Link") != fmt.Sprintf("</fetch/sha256/%s>; rel=\"canonical\"", hash1) {
			t.Errorf("expected Link canonical header, got %s", w.Header().Get("Link"))
		}

		// Verify file exists in cache (sharded)
		shard := hash1[:2]
		if _, err := os.Stat(filepath.Join(cacheDir, "sha256", shard, hash1)); os.IsNotExist(err) {
			t.Errorf("file not found in cache")
		}
	})

	t.Run("Cache Hit", func(t *testing.T) {
		// Should be in cache from previous test
		req := httptest.NewRequest("GET", fmt.Sprintf("/sha256/%s", hash1), nil)
		w := httptest.NewRecorder()

		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if w.Body.String() != "content1" {
			t.Errorf("expected body content1, got %s", w.Body.String())
		}
		// Verify headers on cache hit (MUST Content-Type still present)
		if w.Header().Get("Content-Type") != "application/octet-stream" {
			t.Errorf("expected Content-Type application/octet-stream, got %s", w.Header().Get("Content-Type"))
		}
		if w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Errorf("expected Cache-Control header, got %s", w.Header().Get("Cache-Control"))
		}
	})

	t.Run("Hash Mismatch", func(t *testing.T) {
		// Requesting hash2 but pointing to content1 (hash1)

		defer func() {
			if r := recover(); r != nil {
				// Expected panic
				// We don't verify specific panic because singleflight wraps it.
			} else {
				t.Errorf("expected panic for hash mismatch")
			}
		}()

		req := httptest.NewRequest("GET", fmt.Sprintf("/sha256/%s", hash2), nil)
		req.Header.Set("X-Source-Urls", "\""+origin.URL+"/file1\"")
		w := httptest.NewRecorder()

		h.ServeHTTP(w, req)
	})

	t.Run("Failover", func(t *testing.T) {
		// First URL fails, second succeeds.
		// hash2

		req := httptest.NewRequest("GET", fmt.Sprintf("/sha256/%s", hash2), nil)
		// Header with multiple sources
		// SFV List: "url1", "url2"
		headerVal := fmt.Sprintf("\"%s/fail\", \"%s/file2\"", origin.URL, origin.URL)
		req.Header.Set("X-Source-Urls", headerVal)
		w := httptest.NewRecorder()

		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
		}
		if w.Body.String() != "content2" {
			t.Errorf("expected body content2, got %s", w.Body.String())
		}

		// Verify file exists in cache
		shard := hash2[:2]
		if _, err := os.Stat(filepath.Join(cacheDir, "sha256", shard, hash2)); os.IsNotExist(err) {
			t.Errorf("file not found in cache")
		}
	})

	t.Run("Missing X-Source-Urls", func(t *testing.T) {
		hash3 := sha256Sum([]byte("content3"))
		req := httptest.NewRequest("GET", fmt.Sprintf("/sha256/%s", hash3), nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}
	})

	t.Run("Missing Content-Length", func(t *testing.T) {
		hash := sha256Sum([]byte("content"))
		req := httptest.NewRequest("GET", fmt.Sprintf("/sha256/%s", hash), nil)
		req.Header.Set("X-Source-Urls", "\""+origin.URL+"/no-len\"")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadGateway {
			// It returns 502 Bad Gateway because fetch failed
			t.Errorf("expected 502, got %d", w.Code)
		}
	})

	t.Run("Long hash rejected (255 limit)", func(t *testing.T) {
		longHash := strings.Repeat("a", 256)
		req := httptest.NewRequest("GET", fmt.Sprintf("/sha256/%s", longHash), nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for long hash, got %d", w.Code)
		}
	})

	t.Run("Empty hash short-circuit (no sources needed)", func(t *testing.T) {
		// sha256 of empty input
		emptyHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		req := httptest.NewRequest("GET", fmt.Sprintf("/sha256/%s", emptyHash), nil)
		// Deliberately no X-Source-Urls
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for empty hash, got %d. Body: %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Content-Type") != "application/octet-stream" {
			t.Errorf("expected Content-Type octet-stream for empty, got %s", w.Header().Get("Content-Type"))
		}
		if w.Body.Len() != 0 {
			t.Errorf("expected zero-length body for empty hash, got len=%d", w.Body.Len())
		}
		// Should have materialized the file for future cache hits
		if exists, _ := localRepo.Exists(context.Background(), "sha256", emptyHash); !exists {
			t.Error("empty hash file was not materialized in the repository")
		}
	})
}

func sha256Sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
