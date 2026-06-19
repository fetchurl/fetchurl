package app

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lucasew/fetchurl/internal/errutil"
	"github.com/lucasew/fetchurl/internal/hashutil"
	"github.com/lucasew/fetchurl/internal/httpclient"
	"github.com/lucasew/fetchurl/internal/repository"
	"github.com/schollz/progressbar/v3"
)

type SeedResult struct {
	Processed int
	Seeded    int
	Skipped   int
	Failed    int
}

type SeedOptions struct {
	CacheDir    string
	URLListPath string
	Client      *http.Client
	Logger      *slog.Logger
	ProgressOut io.Writer
}

func SeedCache(ctx context.Context, cacheDir, urlListPath string, client *http.Client) (SeedResult, error) {
	return SeedCacheWithOptions(ctx, SeedOptions{
		CacheDir:    cacheDir,
		URLListPath: urlListPath,
		Client:      client,
	})
}

func SeedCacheWithOptions(ctx context.Context, opts SeedOptions) (SeedResult, error) {
	if opts.Client == nil {
		opts.Client = httpclient.NewClient(nil)
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	if err := os.MkdirAll(opts.CacheDir, 0755); err != nil {
		return SeedResult{}, fmt.Errorf("failed to create cache dir: %w", err)
	}

	urlListFile, err := os.Open(opts.URLListPath)
	if err != nil {
		return SeedResult{}, fmt.Errorf("failed to open url list: %w", err)
	}
	defer func() {
		errutil.ReportError(urlListFile.Close(), "Failed to close URL list file", "path", opts.URLListPath)
	}()

	repo := repository.NewLocalRepository(opts.CacheDir, nil)
	result := SeedResult{}
	scanner := bufio.NewScanner(urlListFile)

	for scanner.Scan() {
		url := strings.TrimSpace(scanner.Text())
		if url == "" {
			continue
		}

		result.Processed++
		seeded, skipped, err := seedURL(ctx, opts.Client, repo, opts.CacheDir, url, opts.Logger, opts.ProgressOut)
		result.Seeded += seeded
		result.Skipped += skipped
		if err != nil {
			result.Failed++
		}
	}

	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("failed to read url list: %w", err)
	}

	if result.Failed > 0 {
		return result, fmt.Errorf("failed to seed %d URLs", result.Failed)
	}

	return result, nil
}

func seedURL(ctx context.Context, client *http.Client, repo *repository.LocalRepository, cacheDir, url string, logger *slog.Logger, progressOut io.Writer) (int, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create request for %s: %w", url, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer func() {
		errutil.ReportError(resp.Body.Close(), "Failed to close response body", "url", url)
	}()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}

	progressWriter, finishProgress := startSeedProgress(logger, progressOut, url, resp.ContentLength)
	var progressErr error
	seeded := 0
	skipped := 0
	hashSummaries := make([]string, 0)
	defer func() {
		finishProgress(seeded, skipped, hashSummaries, progressErr)
	}()

	tmpFile, err := os.CreateTemp(cacheDir, "seed-*")
	if err != nil {
		progressErr = fmt.Errorf("failed to create temp file for %s: %w", url, err)
		return 0, 0, progressErr
	}

	tmpPath := tmpFile.Name()
	defer func() {
		if removeErr := os.Remove(tmpPath); removeErr != nil && !os.IsNotExist(removeErr) {
			errutil.ReportError(removeErr, "Failed to remove seed temp file", "path", tmpPath, "url", url)
		}
	}()
	defer func() {
		errutil.ReportError(tmpFile.Close(), "Failed to close seed temp file", "path", tmpPath, "url", url)
	}()

	hashers, algorithms, err := buildHashers()
	if err != nil {
		return 0, 0, err
	}

	writers := make([]io.Writer, 0, len(hashers)+1)
	writers = append(writers, tmpFile)
	for _, hasher := range hashers {
		writers = append(writers, hasher)
	}
	writers = append(writers, progressWriter)

	if _, err := io.Copy(io.MultiWriter(writers...), resp.Body); err != nil {
		progressErr = fmt.Errorf("failed to read %s: %w", url, err)
		return 0, 0, progressErr
	}

	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		return 0, 0, fmt.Errorf("failed to rewind temp file for %s: %w", url, err)
	}

	for index, algo := range algorithms {
		hash := hex.EncodeToString(hashers[index].Sum(nil))
		hashSummaries = append(hashSummaries, fmt.Sprintf("%s:%s", algo, hash))
		exists, err := repo.Exists(ctx, algo, hash)
		if err != nil {
			progressErr = fmt.Errorf("failed to check cache for %s (%s): %w", url, algo, err)
			return seeded, skipped, progressErr
		}
		if exists {
			skipped++
			continue
		}

		if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
			progressErr = fmt.Errorf("failed to rewind temp file for %s (%s): %w", url, algo, err)
			return seeded, skipped, progressErr
		}

		writer, commit, err := repo.BeginWrite(algo, hash)
		if err != nil {
			progressErr = fmt.Errorf("failed to start cache write for %s (%s): %w", url, algo, err)
			return seeded, skipped, progressErr
		}

		if _, err := io.Copy(writer, tmpFile); err != nil {
			if closeErr := writer.Close(); closeErr != nil {
				progressErr = fmt.Errorf("failed to close cache writer for %s (%s): %w", url, algo, closeErr)
				return seeded, skipped, progressErr
			}
			progressErr = fmt.Errorf("failed to write cache entry for %s (%s): %w", url, algo, err)
			return seeded, skipped, progressErr
		}

		if err := commit(); err != nil {
			progressErr = fmt.Errorf("failed to commit cache entry for %s (%s): %w", url, algo, err)
			return seeded, skipped, progressErr
		}

		seeded++
	}

	return seeded, skipped, nil
}

func buildHashers() ([]hash.Hash, []string, error) {
	algorithms := hashutil.SupportedAlgorithms()
	hashers := make([]hash.Hash, 0, len(algorithms))

	for _, algo := range algorithms {
		hasher, err := hashutil.GetHasher(algo)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create hasher for %s: %w", algo, err)
		}
		hashers = append(hashers, hasher)
	}

	return hashers, algorithms, nil
}

func startSeedProgress(logger *slog.Logger, out io.Writer, url string, contentLength int64) (io.Writer, func(int, int, []string, error)) {
	if out == nil {
		return io.Discard, func(int, int, []string, error) {}
	}

	logger.Info("Seeding URL", "url", url, "content_length", contentLength)

	bar := progressbar.NewOptions64(
		contentLength,
		progressbar.OptionSetWriter(out),
		progressbar.OptionSetDescription("downloading"),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(10),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionClearOnFinish(),
		progressbar.OptionOnCompletion(func() {
			if _, err := fmt.Fprint(out, "\n"); err != nil {
				errutil.ReportError(err, "Failed to print progress completion newline", "url", url)
			}
		}),
	)

	return bar, func(seeded int, skipped int, hashes []string, err error) {
		if err != nil {
			logger.Warn("Failed seeding URL", "url", url, "seeded", seeded, "skipped", skipped, "error", err)
			return
		}
		logger.Info("Finished seeding URL", "url", url, "seeded", seeded, "skipped", skipped, "hashes", hashes)
	}
}
