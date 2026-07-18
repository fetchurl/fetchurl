package app

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
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
	if !strings.Contains(logText, "sha1:"+hashSHA1(content)) || !strings.Contains(logText, "sha256:"+hashSHA256(content)) || !strings.Contains(logText, "sha512:"+hashSHA512(content)) {
		t.Fatalf("missing hashes in seed completion slog in %q", logText)
	}
}

func TestSeedCacheLogsHTTPFailuresWithoutProgress(t *testing.T) {
	urlList := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(urlList, []byte(strings.Join([]string{
		"https://example.test/ok",
		"https://example.test/missing",
	}, "\n")), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	content := []byte("ok-body")
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://example.test/ok":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(content)),
					Header:     make(http.Header),
				}, nil
			case "https://example.test/missing":
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(bytes.NewReader(nil)),
					Header:     make(http.Header),
				}, nil
			default:
				t.Fatalf("unexpected URL: %s", req.URL.String())
				return nil, nil
			}
		}),
	}

	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	cacheDir := filepath.Join(t.TempDir(), "cache")
	result, err := SeedCacheWithOptions(t.Context(), SeedOptions{
		CacheDir:    cacheDir,
		URLListPath: urlList,
		Client:      client,
		Logger:      logger,
		// ProgressOut intentionally nil — regression for silent HTTP failures.
	})
	if err == nil {
		t.Fatal("expected seed error for failed URL")
	}
	if result.Processed != 2 {
		t.Fatalf("Processed = %d, want 2", result.Processed)
	}
	if result.Failed != 1 {
		t.Fatalf("Failed = %d, want 1", result.Failed)
	}
	if result.Seeded != 3 {
		t.Fatalf("Seeded = %d, want 3", result.Seeded)
	}

	logText := logOutput.String()
	if !strings.Contains(logText, "msg=\"Failed seeding URL\"") {
		t.Fatalf("missing failure slog in %q", logText)
	}
	if !strings.Contains(logText, "url=https://example.test/missing") {
		t.Fatalf("missing failed URL in slog %q", logText)
	}
	if !strings.Contains(logText, "unexpected status 404") {
		t.Fatalf("missing status detail in slog %q", logText)
	}
	// Success path still logs when ProgressOut is nil.
	if !strings.Contains(logText, "msg=\"Finished seeding URL\"") || !strings.Contains(logText, "url=https://example.test/ok") {
		t.Fatalf("missing success slog without progress in %q", logText)
	}
}

func TestSeedCacheHonorsContextCancel(t *testing.T) {
	urlList := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(urlList, []byte(strings.Join([]string{
		"https://example.test/a",
		"https://example.test/b",
	}, "\n")), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			// Cancel before the second URL is processed so the loop's ctx check fires.
			cancel()
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte("x"))),
				Header:     make(http.Header),
			}, nil
		}),
	}

	result, err := SeedCacheWithOptions(ctx, SeedOptions{
		CacheDir:    filepath.Join(t.TempDir(), "cache"),
		URLListPath: urlList,
		Client:      client,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if result.Processed != 1 {
		t.Fatalf("Processed = %d, want 1 (stop before second URL)", result.Processed)
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
