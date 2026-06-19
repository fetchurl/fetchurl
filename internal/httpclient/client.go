package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/lucasew/fetchurl/internal/errutil"
)

// NewClient creates an http.Client configured with custom CA certificate + system CAs.
// It also configures a custom dialer to prevent SSRF vulnerabilities by using ValidateIP.
func NewClient(caCert *tls.Certificate) *http.Client {
	_, allowPrivate := os.LookupEnv("FETCHURL_ALLOW_PRIVATE_IPS")
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			return ValidateIP(host, allowPrivate)
		},
	}

	var transport *http.Transport
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	} else {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	transport.DialContext = dialer.DialContext

	if caCert == nil {
		return &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		}
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

	transport.TLSClientConfig = &tls.Config{
		RootCAs: rootCAs,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
}
