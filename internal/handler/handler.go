package handler

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lucasew/fetchurl/internal/errutil"
	"github.com/lucasew/fetchurl/internal/hashutil"
	"github.com/lucasew/fetchurl/internal/httpclient"
	"github.com/lucasew/fetchurl/internal/repository"
	"github.com/shogo82148/go-sfv"
	"golang.org/x/sync/singleflight"
)

type CASHandler struct {
	Local     *repository.LocalRepository
	Client    *http.Client
	Upstreams []string
	AppCtx    context.Context // Application context (from Cobra), not request context
	g         singleflight.Group

	// upstreamHealth caches recent successful /health probes for configured
	// upstream fetchurl servers (keyed by the base URL as provided in config).
	// Used to satisfy the spec rule that downstream servers decide health by
	// checking the dedicated health route.
	upstreamHealth   map[string]time.Time
	upstreamHealthMu sync.Mutex
}

func NewCASHandler(local *repository.LocalRepository, client *http.Client, upstreams []string, appCtx context.Context) *CASHandler {
	if client == nil {
		client = httpclient.NewClient(nil)
	}
	return &CASHandler{
		Local:          local,
		Client:         client,
		Upstreams:      upstreams,
		AppCtx:         appCtx,
		upstreamHealth: make(map[string]time.Time),
	}
}

func (h *CASHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Expected path: /{algo}/{hash} (stripped prefix)
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 2 {
		http.Error(w, "Invalid path format. Expected /{algo}/{hash}", http.StatusBadRequest)
		return
	}
	algo := hashutil.NormalizeAlgo(parts[0])
	hash := parts[1]

	if !hashutil.IsSupported(algo) {
		http.Error(w, fmt.Sprintf("Unsupported hash algorithm: %s", algo), http.StatusBadRequest)
		return
	}

	// Per spec: servers SHOULD reject hashes longer than 255 ASCII characters.
	if len(hash) > 255 {
		http.Error(w, "hash too long (max 255 ASCII characters)", http.StatusBadRequest)
		return
	}

	// Spec SHOULD: servers should satisfy the empty file hash without
	// contacting any source or performing a download. We short-circuit
	// very early (even with no X-Source-Urls) and materialize a 0-byte
	// file for future cache hits / eviction accounting.
	if hashutil.IsEmptyHash(algo, hash) {
		slog.Info("empty hash short-circuit (no download)", "algo", algo, "hash", hash)
		h.serveEmpty(w, algo, hash)
		return
	}

	// 1. Try Local Cache
	exists, err := h.Local.Exists(r.Context(), algo, hash)
	if err != nil {
		errutil.ReportError(err, "Failed to check cache existence")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if exists {
		slog.Info("cache hit", "algo", algo, "hash", hash)
		h.serveFromCache(w, r, algo, hash)
		return
	}

	slog.Info("cache miss", "algo", algo, "hash", hash)

	// 2. Cache Miss -> Fetch & Stream

	// Collect candidates
	candidateSources := h.parseSourceUrls(r.Header)

	// Collect sources to try (Upstreams + Candidates)
	var sourcesToTry []string

	// Add configured upstreams first.
	// Per spec, downstream servers MUST decide healthiness of other servers
	// by checking the status code of the /health route under the same base
	// referenced by FETCHURL_SERVER (200 = healthy).
	// We only forward requests to upstreams that currently report healthy.
	for _, u := range h.Upstreams {
		if !h.isHealthyUpstream(r.Context(), u) {
			slog.Info("skipping unhealthy upstream", "upstream", u)
			continue
		}
		// Construct CAS URL for upstream
		// Assume upstream is a base URL like http://cache.local:8080
		// We need to append /{algo}/{hash}
		// Ensure trailing slash handling
		base := strings.TrimRight(u, "/")
		sourceUrl := fmt.Sprintf("%s/%s/%s", base, algo, hash)
		sourcesToTry = append(sourcesToTry, sourceUrl)
	}

	// Add dynamic sources from headers (shuffled per DESIGN.md constraint 3)
	rand.Shuffle(len(candidateSources), func(i, j int) {
		candidateSources[i], candidateSources[j] = candidateSources[j], candidateSources[i]
	})
	sourcesToTry = append(sourcesToTry, candidateSources...)

	if len(sourcesToTry) == 0 {
		http.Error(w, "Not found and no X-Source-Urls provided", http.StatusNotFound)
		return
	}

	sfKey := algo + ":" + hash

	// Capture if headers were written inside the leader execution
	headersWritten := false

	_, err, shared := h.g.Do(sfKey, func() (interface{}, error) {
		err := h.fetchAndStream(h.AppCtx, w, algo, hash, sourcesToTry, candidateSources, &headersWritten)
		return nil, err
	})

	if err != nil {
		// If error occurred and we haven't written headers yet, send error response
		if !headersWritten {
			errutil.ReportError(err, "Fetch failed")
			http.Error(w, fmt.Sprintf("Failed to fetch: %v", err), http.StatusBadGateway)
		} else {
			// Headers already written, connection might be aborted or partial.
			errutil.ReportError(err, "Fetch failed after headers written")
		}
		return
	}

	// If shared, it means we waited for the leader.
	if shared {
		// Leader finished successfully. Serve from cache.
		h.serveFromCache(w, r, algo, hash)
	}
}

func (h *CASHandler) serveFromCache(w http.ResponseWriter, r *http.Request, algo, hash string) {
	reader, size, err := h.Local.Get(r.Context(), algo, hash)
	if err != nil {
		errutil.ReportError(err, "Failed to get from cache", "hash", hash)
		http.Error(w, "Failed to retrieve from cache", http.StatusInternalServerError)
		return
	}
	defer func() {
		errutil.LogMsg(reader.Close(), "Failed to close cache reader")
	}()

	h.setCacheHeaders(w, algo, hash)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	if _, err := io.Copy(w, reader); err != nil {
		errutil.LogMsg(err, "Failed to copy from cache to response")
	}
}

func (h *CASHandler) fetchAndStream(ctx context.Context, w http.ResponseWriter, algo, hash string, sources []string, candidateSources []string, headersWritten *bool) error {
	for _, source := range sources {
		err := h.tryFetchFromSource(ctx, w, algo, hash, source, candidateSources, headersWritten)
		if err == nil {
			return nil
		}
		errutil.LogMsg(err, "Fetch from source failed", "url", source)
		if *headersWritten {
			return fmt.Errorf("fetch failed after headers already written: %w", err)
		}
	}
	return fmt.Errorf("all sources failed")
}

func (h *CASHandler) tryFetchFromSource(ctx context.Context, w http.ResponseWriter, algo, hash, source string, candidateSources []string, headersWritten *bool) error {
	slog.Info("Fetching from source", "url", source, "hash", hash)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return fmt.Errorf("invalid source URL: %w", err)
	}

	// Forward X-Source-Urls using sfv
	if len(candidateSources) > 0 {
		list := make(sfv.List, len(candidateSources))
		for i, url := range candidateSources {
			list[i] = sfv.Item{Value: url}
		}
		val, err := sfv.EncodeList(list)
		if err == nil {
			req.Header.Set("X-Source-Urls", val)
		} else {
			errutil.LogMsg(err, "Failed to encode X-Source-Urls header")
		}
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		errutil.LogMsg(resp.Body.Close(), "Failed to close response body")
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	if resp.ContentLength == -1 {
		return fmt.Errorf("source did not provide Content-Length")
	}

	// Found it! Start streaming.

	// 1. Prepare Storage
	tmpFile, commit, err := h.Local.BeginWrite(algo, hash)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			errutil.LogMsg(tmpFile.Close(), "Failed to close temp file")
			if f, ok := tmpFile.(*os.File); ok {
				errutil.LogMsg(os.Remove(f.Name()), "Failed to remove temp file", "path", f.Name())
			}
		}
	}()

	// 2. Set Headers
	h.setCacheHeaders(w, algo, hash)
	if resp.ContentLength > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", resp.ContentLength))
	}
	w.WriteHeader(http.StatusOK)
	*headersWritten = true

	// 3. Stream
	hasher, err := hashutil.GetHasher(algo)
	if err != nil {
		return err
	}

	mw := io.MultiWriter(w, tmpFile, hasher)

	written, err := io.Copy(mw, resp.Body)
	if err != nil {
		return fmt.Errorf("streaming failed: %w", err)
	}

	// 4. Verify Hash
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != hash {
		errutil.ReportError(fmt.Errorf("hash mismatch"), "Hash mismatch", "expected", hash, "got", actualHash)
		panic(http.ErrAbortHandler)
	}

	if resp.ContentLength > 0 && written != resp.ContentLength {
		errutil.ReportError(fmt.Errorf("size mismatch"), "Size mismatch", "expected", resp.ContentLength, "got", written)
		panic(http.ErrAbortHandler)
	}

	// 5. Commit
	if err := commit(); err != nil {
		errutil.ReportError(err, "Failed to commit file")
		return err
	}
	committed = true

	return nil // Success
}

func (h *CASHandler) parseSourceUrls(headers http.Header) []string {
	var urls []string
	values := headers.Values("X-Source-Urls")
	if len(values) == 0 {
		return urls
	}

	// Spec: X-Source-Urls SHOULD be no longer than 8192 characters.
	// The server CAN truncate it and load all URLs but the last that is truncated.
	totalLen := 0
	for _, v := range values {
		totalLen += len(v)
	}

	list, err := sfv.DecodeList(values)
	if err != nil {
		errutil.LogMsg(err, "Failed to parse X-Source-Urls header")
		return urls
	}

	for _, item := range list {
		if s, ok := item.Value.(string); ok {
			urls = append(urls, s)
		}
	}

	if totalLen > 8192 && len(urls) > 1 {
		// Truncate by dropping the final entry in the list.
		urls = urls[:len(urls)-1]
	}
	return urls
}

func (h *CASHandler) setCacheHeaders(w http.ResponseWriter, algo, hash string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Link", fmt.Sprintf("</fetch/%s/%s>; rel=\"canonical\"", algo, hash))
}

// serveEmpty satisfies a request for a known-empty hash (per spec SHOULD).
// It sets the proper headers (including the MUST Content-Type), writes a
// zero-length body, and best-effort materializes the empty file on disk
// so that subsequent requests and the eviction manager see it.
func (h *CASHandler) serveEmpty(w http.ResponseWriter, algo, hash string) {
	h.setCacheHeaders(w, algo, hash)
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)

	// Best-effort: ensure a 0-byte file exists in the CAS store.
	// Ignore errors — an empty file is easy to recreate and not critical.
	if exists, _ := h.Local.Exists(context.Background(), algo, hash); !exists {
		tmp, commit, err := h.Local.BeginWrite(algo, hash)
		if err != nil {
			errutil.LogMsg(err, "BeginWrite failed for empty hash materialization")
			return
		}
		// Do not Close the tmp ourselves — commit() is responsible for
		// closing the temp file before rename (see local.go:BeginWrite).
		// Writing zero bytes is implicit (we never Write anything).
		if cerr := commit(); cerr != nil {
			errutil.LogMsg(cerr, "failed to commit empty file")
		}
		_ = tmp // tmp may be closed by commit; avoid unused warning
	}
}

// isHealthyUpstream actively checks the dedicated health route of a
// configured upstream fetchurl server (the base as given to --upstream
// or FETCHURL_UPSTREAM). It returns true only on HTTP 200.
//
// It implements a small TTL cache so we don't hammer health on every miss.
// Per spec: "Downstream servers MUST decide about the healthiness of the
// server by checking the status code of the health route. 200 = OK."
//
// Only configured upstreams (daisy-chained fetchurl servers) go through
// this filter. Original source URLs from X-Source-Urls are not health-checked.
func (h *CASHandler) isHealthyUpstream(ctx context.Context, upstreamBase string) bool {
	base := strings.TrimRight(upstreamBase, "/")
	healthURL := base + "/health"

	const healthTTL = 30 * time.Second

	h.upstreamHealthMu.Lock()
	if last, ok := h.upstreamHealth[base]; ok && time.Since(last) < healthTTL {
		h.upstreamHealthMu.Unlock()
		return true
	}
	h.upstreamHealthMu.Unlock()

	// Perform an active health decision using the spec-defined route.
	probeCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return false
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		slog.Info("upstream health probe failed", "upstream", base, "error", err)
		return false
	}

	statusOK := resp.StatusCode == http.StatusOK
	closeErr := resp.Body.Close()
	errutil.LogMsg(closeErr, "failed to close upstream health response body", "upstream", base)

	if statusOK {
		h.upstreamHealthMu.Lock()
		h.upstreamHealth[base] = time.Now()
		h.upstreamHealthMu.Unlock()
	} else {
		slog.Info("upstream health check returned non-200", "upstream", base, "status", resp.StatusCode)
	}

	return statusOK
}
