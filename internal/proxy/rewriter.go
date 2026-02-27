package proxy

import (
	"net/http"

	"github.com/lucasew/fetchurl/internal/handler"
	"github.com/lucasew/fetchurl/internal/utils"
)

var hashes = utils.ThreadSafeMap[string, utils.Hash]{}

func GetRewriter(cas *handler.CASHandler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", NewHandler(cas.Client))
	mux.Handle("/npm/", http.StripPrefix("/npm", NewHandler(cas.Client)))
	return mux
}
