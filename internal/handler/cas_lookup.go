package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/lucasew/fetchurl/internal/hashutil"
	"github.com/lucasew/fetchurl/internal/utils"
)

// SourceMap maps "algo:hash" to a list of source URLs.
type SourceMap = utils.ThreadSafeMap[string, []string]

// CASLookupHandler serves CAS requests using a pre-populated source map
// instead of X-Source-Urls headers.
type CASLookupHandler struct {
	CAS     *CASHandler
	Sources *SourceMap
}

func NewCASLookupHandler(cas *CASHandler, sources *SourceMap) *CASLookupHandler {
	return &CASLookupHandler{
		CAS:     cas,
		Sources: sources,
	}
}

func (h *CASLookupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 2 {
		http.Error(w, "Invalid path format. Expected /{algo}/{hash}", http.StatusBadRequest)
		return
	}

	id := utils.Hash{
		Algo: hashutil.NormalizeAlgo(parts[0]),
		Hash: parts[1],
	}

	if !hashutil.IsSupported(id.Algo) {
		http.Error(w, fmt.Sprintf("Unsupported hash algorithm: %s", id.Algo), http.StatusBadRequest)
		return
	}

	sources, ok := h.Sources.Get(id.String())
	if !ok {
		http.Error(w, "Hash not found in source map", http.StatusNotFound)
		return
	}

	h.CAS.Serve(w, r, id, sources)
}
