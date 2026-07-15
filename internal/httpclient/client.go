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
// Callers that need a custom DialContext (e.g. SSRF IP filtering on the
// server) should start from NewTransport and replace DialContext.
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
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
func New() *http.Client {
	return &http.Client{
		Transport: NewTransport(),
	}
}
