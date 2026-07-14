package app

import (
	"net/http"
	"net/http/httptest"
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
