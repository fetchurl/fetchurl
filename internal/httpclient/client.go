package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"time"

	"github.com/fetchurl/fetchurl/internal/errutil"
)

// NewClient creates an http.Client configured with custom CA certificate + system CAs.
// If caCert is nil, returns http.DefaultClient for backward compatibility.
func NewClient(caCert *tls.Certificate) *http.Client {
	if caCert == nil {
		return http.DefaultClient
	}

	// Load system cert pool
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}

	// Add custom CA to the cert pool
	if len(caCert.Certificate) > 0 {
		cert, err := x509.ParseCertificate(caCert.Certificate[0])
		if err == nil {
			rootCAs.AddCert(cert)
		} else {
			errutil.ReportError(err, "Failed to parse custom CA certificate")
		}
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: rootCAs,
			},
		},
		Timeout: 30 * time.Second,
	}
}
