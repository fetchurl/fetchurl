package fetchurl

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"

	"github.com/fetchurl/fetchurl/internal/errutil"
	"github.com/fetchurl/fetchurl/internal/hashutil"
	"github.com/shogo82148/go-sfv"
)

var (
	// ErrUnsupportedAlgorithm is returned when the requested hash algorithm is not supported.
	ErrUnsupportedAlgorithm = errors.New("unsupported algorithm")

	// ErrHashMismatch is returned when the downloaded content does not match the expected hash.
	ErrHashMismatch = errors.New("hash mismatch")

	// ErrPartialWrite is returned when data was already written to Out before a failure occurred,
	// making fallback to another source unsafe.
	ErrPartialWrite = errors.New("partial write")

	// ErrAllSourcesFailed is returned when no server or direct source could provide the content.
	ErrAllSourcesFailed = errors.New("all sources failed")

	// ErrMissingSourceURLs is returned when no source URLs are provided.
	// Per the spec, clients MUST know the hash and source URLs before connecting
	// to a server (they are sent via X-Source-Urls) or falling back to direct fetch.
	ErrMissingSourceURLs = errors.New("source URLs are required")
)

// HTTPStatusError is returned when a source responds with a non-200 status code.
type HTTPStatusError struct {
	StatusCode int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("unexpected status %d", e.StatusCode)
}

type Fetcher struct {
	Client  *http.Client
	Servers []string
}

type FetchOptions struct {
	Algo string
	Hash string
	URLs []string
	Out  io.Writer
}

func NewFetcher(client *http.Client) *Fetcher {
	if client == nil {
		client = http.DefaultClient
	}

	var servers []string
	envServer := os.Getenv("FETCHURL_SERVER")
	if envServer != "" {
		envServer = strings.TrimSpace(envServer)
		if strings.HasPrefix(envServer, "\"") {
			list, err := sfv.DecodeList([]string{envServer})
			if err != nil {
				errutil.LogMsg(err, "Failed to parse FETCHURL_SERVER")
			} else {
				for _, item := range list {
					if s, ok := item.Value.(string); ok {
						servers = append(servers, s)
					}
				}
			}
		} else {
			servers = append(servers, envServer)
		}
	}

	return &Fetcher{
		Client:  client,
		Servers: servers,
	}
}

func (f *Fetcher) Fetch(ctx context.Context, opts FetchOptions) error {
	if len(opts.URLs) == 0 {
		return ErrMissingSourceURLs
	}

	if !hashutil.IsSupported(opts.Algo) {
		return fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, opts.Algo)
	}

	cw := &countingWriter{Writer: opts.Out}
	var lastErr error

	// 1. Try Servers
	for _, server := range f.Servers {
		lastErr = f.fetchFromServer(ctx, server, opts.Algo, opts.Hash, opts.URLs, cw)
		if lastErr == nil {
			return nil
		}
		errutil.LogMsg(lastErr, "Failed to fetch from server", "server", server)
		if cw.N > 0 {
			return fmt.Errorf("%w: %w", ErrPartialWrite, lastErr)
		}
	}

	// 2. Fallback to Direct Download
	// Shuffle direct sources per spec: "A source URL SHOULD be chosen randomly
	// at fetch time, like a mirror list".
	direct := make([]string, len(opts.URLs))
	copy(direct, opts.URLs)
	rand.Shuffle(len(direct), func(i, j int) {
		direct[i], direct[j] = direct[j], direct[i]
	})
	for _, url := range direct {
		lastErr = f.fetchDirect(ctx, url, opts.Algo, opts.Hash, cw)
		if lastErr == nil {
			return nil
		}
		errutil.LogMsg(lastErr, "Failed to fetch from source", "url", url)
		if cw.N > 0 {
			return fmt.Errorf("%w: %w", ErrPartialWrite, lastErr)
		}
	}

	if lastErr != nil {
		return fmt.Errorf("%w: %w", ErrAllSourcesFailed, lastErr)
	}
	return ErrAllSourcesFailed
}

type countingWriter struct {
	Writer io.Writer
	N      int64
}

func (c *countingWriter) Write(p []byte) (n int, err error) {
	n, err = c.Writer.Write(p)
	c.N += int64(n)
	return n, err
}

func (f *Fetcher) fetchFromServer(ctx context.Context, server, algo, hashStr string, sourceUrls []string, out io.Writer) error {
	base := strings.TrimRight(server, "/")
	u := fmt.Sprintf("%s/%s/%s", base, algo, hashStr)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}

	if len(sourceUrls) > 0 {
		list := make(sfv.List, len(sourceUrls))
		for i, url := range sourceUrls {
			list[i] = sfv.Item{Value: url}
		}
		val, err := sfv.EncodeList(list)
		if err != nil {
			return fmt.Errorf("failed to encode X-Source-Urls: %w", err)
		}
		req.Header.Set("X-Source-Urls", val)
	}

	return f.doRequest(req, algo, hashStr, out)
}

func (f *Fetcher) fetchDirect(ctx context.Context, url, algo, hashStr string, out io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	return f.doRequest(req, algo, hashStr, out)
}

func (f *Fetcher) doRequest(req *http.Request, algo, expectedHash string, out io.Writer) error {
	resp, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		errutil.LogMsg(resp.Body.Close(), "Failed to close response body")
	}()

	if resp.StatusCode != http.StatusOK {
		return &HTTPStatusError{StatusCode: resp.StatusCode}
	}

	hasher, err := hashutil.GetHasher(algo)
	if err != nil {
		return err
	}
	mw := io.MultiWriter(out, hasher)

	if _, err := io.Copy(mw, resp.Body); err != nil {
		return err
	}

	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != expectedHash {
		return fmt.Errorf("%w: expected %s, got %s", ErrHashMismatch, expectedHash, actualHash)
	}

	return nil
}
