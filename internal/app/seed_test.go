package app

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestSeedCache(t *testing.T) {
	contentA := []byte("alpha")
	contentB := []byte("beta")

	urlList := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(urlList, []byte(strings.Join([]string{
		"https://example.test/a",
		"",
		"https://example.test/b",
	}, "\n")), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var body []byte
			switch req.URL.String() {
			case "https://example.test/a":
				body = contentA
			case "https://example.test/b":
				body = contentB
			default:
				t.Fatalf("unexpected URL: %s", req.URL.String())
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")
	result, err := SeedCache(t.Context(), cacheDir, urlList, client)
	if err != nil {
		t.Fatalf("SeedCache returned error: %v", err)
	}

	if result.Processed != 2 {
		t.Fatalf("Processed = %d, want 2", result.Processed)
	}
	if result.Seeded != 6 {
		t.Fatalf("Seeded = %d, want 6", result.Seeded)
	}
	if result.Failed != 0 {
		t.Fatalf("Failed = %d, want 0", result.Failed)
	}

	assertCachedFile(t, cacheDir, "sha1", hashSHA1(contentA), contentA)
	assertCachedFile(t, cacheDir, "sha256", hashSHA256(contentA), contentA)
	assertCachedFile(t, cacheDir, "sha512", hashSHA512(contentA), contentA)
	assertCachedFile(t, cacheDir, "sha1", hashSHA1(contentB), contentB)
	assertCachedFile(t, cacheDir, "sha256", hashSHA256(contentB), contentB)
	assertCachedFile(t, cacheDir, "sha512", hashSHA512(contentB), contentB)
}

func TestSeedCacheReportsProgress(t *testing.T) {
	content := []byte("progress")

	urlList := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(urlList, []byte("https://example.test/progress\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: int64(len(content)),
				Body:          io.NopCloser(bytes.NewReader(content)),
				Header:        make(http.Header),
			}, nil
		}),
	}

	var progressLog bytes.Buffer
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	cacheDir := filepath.Join(t.TempDir(), "cache")
	if _, err := SeedCacheWithOptions(t.Context(), SeedOptions{
		CacheDir:    cacheDir,
		URLListPath: urlList,
		Client:      client,
		Logger:      logger,
		ProgressOut: &progressLog,
	}); err != nil {
		t.Fatalf("SeedCacheWithOptions returned error: %v", err)
	}

	if progressLog.Len() == 0 {
		t.Fatal("expected progress bar output")
	}

	logText := logOutput.String()
	if !strings.Contains(logText, "msg=\"Seeding URL\"") || !strings.Contains(logText, "url=https://example.test/progress") {
		t.Fatalf("missing seed start slog in %q", logText)
	}
	if !strings.Contains(logText, "msg=\"Finished seeding URL\"") || !strings.Contains(logText, "seeded=3") || !strings.Contains(logText, "skipped=0") {
		t.Fatalf("missing seed completion slog in %q", logText)
	}
}

func assertCachedFile(t *testing.T, cacheDir, algo, hash string, want []byte) {
	t.Helper()

	path := filepath.Join(cacheDir, algo, hash[:2], hash)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) failed: %v", path, err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("cached content mismatch for %s/%s", algo, hash)
	}
}

func hashSHA1(data []byte) string {
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:])
}

func hashSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashSHA512(data []byte) string {
	sum := sha512.Sum512(data)
	return hex.EncodeToString(sum[:])
}
