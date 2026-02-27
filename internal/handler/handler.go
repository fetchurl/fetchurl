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

	"github.com/lucasew/fetchurl/internal/errutil"
	"github.com/lucasew/fetchurl/internal/hashutil"
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
}

func NewCASHandler(local *repository.LocalRepository, client *http.Client, upstreams []string, appCtx context.Context) *CASHandler {
	if client == nil {
		client = http.DefaultClient
	}
	return &CASHandler{
		Local:     local,
		Client:    client,
		Upstreams: upstreams,
		AppCtx:    appCtx,
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

	// Collect candidates from headers
	candidateSources := h.parseSourceUrls(r.Header)

	// Collect sources to try (Upstreams + Candidates)
	var sourcesToTry []string

	// Add configured upstreams first
	for _, u := range h.Upstreams {
		base := strings.TrimRight(u, "/")
		sourceUrl := fmt.Sprintf("%s/api/fetchurl/%s/%s", base, algo, hash)
		sourcesToTry = append(sourcesToTry, sourceUrl)
	}

	// Add dynamic sources from headers (shuffled per DESIGN.md constraint 3)
	rand.Shuffle(len(candidateSources), func(i, j int) {
		candidateSources[i], candidateSources[j] = candidateSources[j], candidateSources[i]
	})
	sourcesToTry = append(sourcesToTry, candidateSources...)

	h.Serve(w, r, algo, hash, sourcesToTry, candidateSources)
}

// Serve handles the fetch logic for a given algo/hash with explicit source URLs.
// It checks the local cache first, then tries the provided sources.
// candidateSources are forwarded as X-Source-Urls to upstreams.
func (h *CASHandler) Serve(w http.ResponseWriter, r *http.Request, algo, hash string, sourcesToTry, candidateSources []string) {
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

	if len(sourcesToTry) == 0 {
		http.Error(w, "Not found and no source URLs available", http.StatusNotFound)
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
		if !headersWritten {
			errutil.ReportError(err, "Fetch failed")
			http.Error(w, fmt.Sprintf("Failed to fetch: %v", err), http.StatusBadGateway)
		} else {
			errutil.ReportError(err, "Fetch failed after headers written")
		}
		return
	}

	// If shared, it means we waited for the leader.
	if shared {
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
	return urls
}

func (h *CASHandler) setCacheHeaders(w http.ResponseWriter, algo, hash string) {
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Link", fmt.Sprintf("</fetch/%s/%s>; rel=\"canonical\"", algo, hash))
}
