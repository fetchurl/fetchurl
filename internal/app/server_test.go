package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestNewServerHealthRoute(t *testing.T) {
	server, cleanup, err := NewServer(t.Context(), Config{
		Port:             8080,
		CacheDir:         t.TempDir(),
		EvictionInterval: time.Minute,
		EvictionStrategy: "lru",
	})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/fetchurl/health", nil)
	rec := httptest.NewRecorder()

	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/fetchurl/health status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestNewServerTimeouts(t *testing.T) {
	server, cleanup, err := NewServer(t.Context(), Config{
		Port:             8080,
		CacheDir:         t.TempDir(),
		EvictionInterval: time.Minute,
		EvictionStrategy: "lru",
	})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	defer cleanup()

	if server.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", server.ReadHeaderTimeout)
	}
	if server.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %v, want 120s", server.IdleTimeout)
	}
	// WriteTimeout must stay zero so large CAS streams are not aborted mid-transfer.
	if server.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want 0 (unlimited for large streams)", server.WriteTimeout)
	}
}

func TestNewOutboundClientHeaderTimeout(t *testing.T) {
	client := newOutboundClient(true)
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport type = %T, want *http.Transport", client.Transport)
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 30s (must match httpclient.NewTransport)", tr.ResponseHeaderTimeout)
	}
	if client.Timeout != 0 {
		t.Errorf("Client.Timeout = %v, want 0 (large CAS streams)", client.Timeout)
	}
}

// NewServer must fail closed when the cache cannot be indexed — serving with
// a broken eviction view under-counts usage and can skip capacity limits.
func TestNewServerLoadInitialStateFailure(t *testing.T) {
	cacheDir := t.TempDir()
	// MkdirAll succeeds; walk fails because the directory is unreadable.
	if err := os.Chmod(cacheDir, 0); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(cacheDir, 0o755); err != nil {
			t.Errorf("restore Chmod: %v", err)
		}
	})

	server, cleanup, err := NewServer(t.Context(), Config{
		Port:             8080,
		CacheDir:         cacheDir,
		EvictionInterval: time.Minute,
		EvictionStrategy: "lru",
	})
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatal("NewServer: want error when LoadInitialState cannot walk cache")
	}
	if server != nil {
		t.Error("NewServer: server must be nil on error")
	}
}
