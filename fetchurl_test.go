package fetchurl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shogo82148/go-sfv"
)

func sha256Sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestFetcher(t *testing.T) {
	content := []byte("test content")
	hash := sha256Sum(content)

	t.Run("Direct Download Success", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := w.Write(content); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		}))
		defer ts.Close()

		f := NewFetcher(nil)
		var out bytes.Buffer
		err := f.Fetch(t.Context(), FetchOptions{
			Algo: "sha256",
			Hash: hash,
			URLs: []string{ts.URL},
			Out:  &out,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.String() != string(content) {
			t.Errorf("got %q, want %q", out.String(), string(content))
		}
	})

	t.Run("Direct Download Hash Mismatch", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := w.Write([]byte("wrong content")); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		}))
		defer ts.Close()

		f := NewFetcher(nil)
		var out bytes.Buffer
		err := f.Fetch(t.Context(), FetchOptions{
			Algo: "sha256",
			Hash: hash,
			URLs: []string{ts.URL},
			Out:  &out,
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrPartialWrite) {
			t.Errorf("expected ErrPartialWrite, got %v", err)
		}
		if !errors.Is(err, ErrHashMismatch) {
			t.Errorf("expected ErrHashMismatch, got %v", err)
		}
	})

	t.Run("Server Fetch Success", func(t *testing.T) {
		// Mock Source
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("Should not hit source if server succeeds")
		}))
		defer source.Close()

		// Mock FetchURL Server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != fmt.Sprintf("/api/fetchurl/sha256/%s", hash) {
				t.Errorf("unexpected path: %s", r.URL.Path)
				w.WriteHeader(404)
				return
			}
			// Verify X-Source-Urls header
			val := r.Header.Get("X-Source-Urls")
			list, err := sfv.DecodeList([]string{val})
			if err != nil {
				t.Errorf("failed to decode X-Source-Urls: %v", err)
			}
			found := false
			for _, item := range list {
				if s, ok := item.Value.(string); ok && s == source.URL {
					found = true
				}
			}
			if !found {
				t.Errorf("X-Source-Urls missing source URL, got %v", val)
			}

			if _, err := w.Write(content); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		}))
		defer server.Close()

		t.Setenv("FETCHURL_SERVER", fmt.Sprintf("\"%s/api/fetchurl\"", server.URL))
		f := NewFetcher(nil)
		var out bytes.Buffer
		err := f.Fetch(t.Context(), FetchOptions{
			Algo: "sha256",
			Hash: hash,
			URLs: []string{source.URL},
			Out:  &out,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.String() != string(content) {
			t.Errorf("got %q, want %q", out.String(), string(content))
		}
	})

	t.Run("Server Fail Fallback", func(t *testing.T) {
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := w.Write(content); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		}))
		defer source.Close()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
		}))
		defer server.Close()

		t.Setenv("FETCHURL_SERVER", fmt.Sprintf("\"%s/api/fetchurl\"", server.URL))
		f := NewFetcher(nil)
		var out bytes.Buffer
		err := f.Fetch(t.Context(), FetchOptions{
			Algo: "sha256",
			Hash: hash,
			URLs: []string{source.URL},
			Out:  &out,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.String() != string(content) {
			t.Errorf("got %q, want %q", out.String(), string(content))
		}
	})

	t.Run("Partial Download No Fallback", func(t *testing.T) {
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := w.Write(content); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		}))
		defer source.Close()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := w.Write([]byte("partial")); err != nil {
				t.Errorf("failed to write response: %v", err)
			}
		}))
		defer server.Close()

		t.Setenv("FETCHURL_SERVER", fmt.Sprintf("\"%s/api/fetchurl\"", server.URL))
		f := NewFetcher(nil)
		var out bytes.Buffer
		err := f.Fetch(t.Context(), FetchOptions{
			Algo: "sha256",
			Hash: hash,
			URLs: []string{source.URL},
			Out:  &out,
		})

		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrPartialWrite) {
			t.Errorf("expected ErrPartialWrite, got %v", err)
		}

		if out.String() != "partial" {
			t.Errorf("got %q, want %q", out.String(), "partial")
		}
	})

	t.Run("Unsupported Algorithm", func(t *testing.T) {
		f := NewFetcher(nil)
		var out bytes.Buffer
		err := f.Fetch(t.Context(), FetchOptions{
			Algo: "md4",
			Hash: "abc",
			URLs: []string{"http://example.com"},
			Out:  &out,
		})
		if !errors.Is(err, ErrUnsupportedAlgorithm) {
			t.Errorf("expected ErrUnsupportedAlgorithm, got %v", err)
		}
	})

	t.Run("All Sources Failed", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
		}))
		defer server.Close()

		t.Setenv("FETCHURL_SERVER", fmt.Sprintf("\"%s/api/fetchurl\"", server.URL))
		f := NewFetcher(nil)
		var out bytes.Buffer
		err := f.Fetch(t.Context(), FetchOptions{
			Algo: "sha256",
			Hash: hash,
			URLs: []string{server.URL},
			Out:  &out,
		})
		if !errors.Is(err, ErrAllSourcesFailed) {
			t.Errorf("expected ErrAllSourcesFailed, got %v", err)
		}
	})

	t.Run("HTTP Status Error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(403)
		}))
		defer server.Close()

		t.Setenv("FETCHURL_SERVER", fmt.Sprintf("\"%s/api/fetchurl\"", server.URL))
		f := NewFetcher(nil)
		var out bytes.Buffer
		err := f.Fetch(t.Context(), FetchOptions{
			Algo: "sha256",
			Hash: hash,
			URLs: []string{server.URL},
			Out:  &out,
		})
		var httpErr *HTTPStatusError
		if !errors.As(err, &httpErr) {
			t.Fatalf("expected HTTPStatusError, got %T: %v", err, err)
		}
		if httpErr.StatusCode != 403 {
			t.Errorf("expected status 403, got %d", httpErr.StatusCode)
		}
	})
}
