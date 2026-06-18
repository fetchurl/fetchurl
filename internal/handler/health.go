package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lucasew/fetchurl/internal/errutil"
)

type UpstreamHealthChecker struct {
	Client           *http.Client
	upstreamHealth   map[string]time.Time
	upstreamHealthMu sync.Mutex
}

func NewUpstreamHealthChecker(client *http.Client) *UpstreamHealthChecker {
	return &UpstreamHealthChecker{
		Client:         client,
		upstreamHealth: make(map[string]time.Time),
	}
}

// IsHealthy actively checks the dedicated health route of a
// configured upstream fetchurl server (the base as given to --upstream
// or FETCHURL_UPSTREAM). It returns true only on HTTP 200.
//
// It implements a small TTL cache so we don't hammer health on every miss.
// Per spec: "Downstream servers MUST decide about the healthiness of the
// server by checking the status code of the health route. 200 = OK."
//
// Only configured upstreams (daisy-chained fetchurl servers) go through
// this filter. Original source URLs from X-Source-Urls are not health-checked.
func (h *UpstreamHealthChecker) IsHealthy(ctx context.Context, upstreamBase string) bool {
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
