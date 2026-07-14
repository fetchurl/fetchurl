// Package httpclient provides shared HTTP client construction for outbound
// fetchurl requests (CLI get/seed and the library Fetcher default).
package httpclient

import (
	"net"
	"net/http"
	"time"
)

// New returns an *http.Client suitable for downloading CAS objects.
//
// It does not set Client.Timeout (that covers the entire transfer and would
// abort multi-GB bodies on slow links). Dial, TLS handshake, and response-
// header waits are bounded so stalled peers fail without killing long streams.
func New() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
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
		},
	}
}
