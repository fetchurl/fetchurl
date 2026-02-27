package proxy

import (
	"net/http"

	"github.com/lucasew/fetchurl/internal/handler"
)

func GetRewriter(cas *handler.CASHandler, sources *SourceMap) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", NewHandler(cas.Client, sources))
	mux.Handle("/npm/", http.StripPrefix("/npm", NewHandler(cas.Client, sources)))
	return mux
}
