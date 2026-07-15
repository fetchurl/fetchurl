package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fetchurl/fetchurl/internal/repository"
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

	t.Run("Path traversal hash rejected", func(t *testing.T) {
		// Single path segment ".." is accepted by the /{algo}/{hash} split, but
		// filepath.Join(cacheDir, "sha256", "..", "..") resolves to the parent of
		// the cache root. Digest validation must reject it before any FS access.
		req := httptest.NewRequest("GET", "/sha256/..", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for path traversal hash, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("Non-hex digest rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/sha256/zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for non-hex digest, got %d", w.Code)
		}
	})

	t.Run("Wrong-length hex digest rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/sha256/deadbeef", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for short digest, got %d", w.Code)
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
		exists, err := localRepo.Exists(t.Context(), "sha256", emptyHash)
		if err != nil {
			t.Fatalf("Exists after empty short-circuit: %v", err)
		}
		if !exists {
			t.Error("empty hash file was not materialized in the repository")
		}
	})

	t.Run("Uppercase digest normalizes to lowercase cache key", func(t *testing.T) {
		emptyLower := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		emptyUpper := strings.ToUpper(emptyLower)
		req := httptest.NewRequest("GET", fmt.Sprintf("/sha256/%s", emptyUpper), nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for uppercase empty hash, got %d. Body: %s", w.Code, w.Body.String())
		}
		// Must land under the lowercase path so mixed-case clients share cache entries.
		exists, err := localRepo.Exists(t.Context(), "sha256", emptyLower)
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if !exists {
			t.Error("uppercase request did not materialize lowercase cache entry")
		}
		if w.Header().Get("Link") != fmt.Sprintf("</fetch/sha256/%s>; rel=\"canonical\"", emptyLower) {
			t.Errorf("Link header should use lowercase digest, got %s", w.Header().Get("Link"))
		}
	})
}

func sha256Sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestNewCASHandlerNilClientNotDefault(t *testing.T) {
	// Production injects an SSRF-aware client; nil must still get dial/header
	// bounds via httpclient.New, never the unbounded http.DefaultClient.
	h := NewCASHandler(repository.NewLocalRepository(t.TempDir(), nil), nil, nil, t.Context())
	if h.Client == nil {
		t.Fatal("Client is nil")
	}
	if h.Client == http.DefaultClient {
		t.Fatal("nil client must not fall back to http.DefaultClient")
	}
	if h.Client.Timeout != 0 {
		t.Errorf("Client.Timeout = %v, want 0 (no full-body deadline)", h.Client.Timeout)
	}
	tr, ok := h.Client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport type = %T, want *http.Transport", h.Client.Transport)
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 30s", tr.ResponseHeaderTimeout)
	}
}

func TestUpstreamHealthNegativeCache(t *testing.T) {
	// Unhealthy /health results must be cached so cache-miss storms do not
	// re-issue a multi-second probe on every request.
	var probes atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			probes.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// CAS object path should never be reached while unhealthy.
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(upstream.Close)

	localRepo := repository.NewLocalRepository(t.TempDir(), nil)
	h := NewCASHandler(localRepo, nil, []string{upstream.URL}, t.Context())

	hash := sha256Sum([]byte("upstream-health-negative-cache"))
	// Two independent cache misses: only the first should probe /health.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sha256/%s", hash), nil)
		// No X-Source-Urls: with only an unhealthy upstream this is a 404.
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("request %d: status = %d, want 404 (unhealthy upstream + no sources)", i, w.Code)
		}
	}

	if got := probes.Load(); got != 1 {
		t.Fatalf("health probes = %d, want 1 (negative result must be TTL-cached)", got)
	}

	// Positive path still works and is also cached for subsequent lookups.
	var okProbes atomic.Int32
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			okProbes.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(healthy.Close)

	h2 := NewCASHandler(repository.NewLocalRepository(t.TempDir(), nil), nil, []string{healthy.URL}, t.Context())
	// Direct unit checks of the helper (avoids needing a full CAS object).
	if !h2.isHealthyUpstream(t.Context(), healthy.URL) {
		t.Fatal("expected healthy upstream after 200 /health")
	}
	if !h2.isHealthyUpstream(t.Context(), healthy.URL) {
		t.Fatal("expected second lookup to stay healthy from cache")
	}
	if got := okProbes.Load(); got != 1 {
		t.Fatalf("healthy probes = %d, want 1 (positive result must be TTL-cached)", got)
	}
}
