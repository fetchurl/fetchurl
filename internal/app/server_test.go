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
