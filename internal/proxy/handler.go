package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/lucasew/fetchurl/internal/utils"
)

// SourceMap maps "algo:hash" to a list of source URLs.
type SourceMap = utils.ThreadSafeMap[string, []string]

type Handler struct {
	client  *http.Client
	sources *SourceMap
}

func NewHandler(client *http.Client, sources *SourceMap) *Handler {
	if client == nil {
		client = &http.Client{}
	}
	return &Handler{
		client:  client,
		sources: sources,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	registryName := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
	restOfRoute := strings.TrimPrefix(r.URL.Path, "/"+registryName)
	upstream, ok := REGISTRY_REMAPS[registryName]
	slog.Info("Received registry request", "registry", registryName, "rest_of_route", restOfRoute)
	if !ok {
		http.Error(w, "upstream not found", http.StatusNotFound)
		return
	}
	if restOfRoute == "" {
		restOfRoute = "/"
	}

	// Create a new request to the target registry
	targetURL := upstream + restOfRoute
	slog.Info("Proxying request", "registry", registryName, "target_url", targetURL)
	req, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}

	// Copy headers from the original request
	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Send the request to the target registry
	resp, err := h.client.Do(req)
	if err != nil {
		http.Error(w, "Failed to fetch from registry", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Copy the response back to the original client
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
