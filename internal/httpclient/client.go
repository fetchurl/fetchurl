// Package httpclient provides shared HTTP client construction for outbound
// fetchurl requests (CLI get/seed, library Fetcher default, and the CAS
// server's SSRF-aware transport base).
package httpclient

import (
	"net"
	"net/http"
	"time"
)

// NewTransport returns an *http.Transport with dial, TLS handshake, and
// response-header waits bounded for CAS downloads. It does not set a
// full-body Client.Timeout — multi-GB streams on slow links must not be
// aborted by a global deadline.
//
// Proxy is intentionally left nil. Callers that install a custom DialContext
// for SSRF IP filtering (the CAS server) must dial the real peer; an
// HTTP(S)_PROXY hop would connect only to the proxy and let the proxy reach
// blocked addresses. CLI and library clients that should honor environment
// proxies use New, which sets ProxyFromEnvironment on a copy of this base.
//
// Callers that need a custom DialContext should start from NewTransport and
// replace DialContext.
func NewTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}
}

// New returns an *http.Client suitable for downloading CAS objects.
//
// It does not set Client.Timeout (that covers the entire transfer and would
// abort multi-GB bodies on slow links). Dial, TLS handshake, and response-
// header waits are bounded so stalled peers fail without killing long streams.
// HTTP(S)_PROXY / NO_PROXY from the environment are honored for CLI and
// library fetches; the server's SSRF-aware transport uses NewTransport
// without a proxy so DialContext filters apply to the origin.
func New() *http.Client {
	tr := NewTransport()
	tr.Proxy = http.ProxyFromEnvironment
	return &http.Client{
		Transport: tr,
	}
}
