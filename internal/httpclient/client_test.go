package httpclient

import (
	"net/http"
	"testing"
	"time"
)

func TestNewTransportBounds(t *testing.T) {
	tr := NewTransport()
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 30s", tr.ResponseHeaderTimeout)
	}
	if tr.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v, want 10s", tr.TLSHandshakeTimeout)
	}
	if tr.ExpectContinueTimeout != 1*time.Second {
		t.Errorf("ExpectContinueTimeout = %v, want 1s", tr.ExpectContinueTimeout)
	}
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", tr.IdleConnTimeout)
	}
	if tr.DialContext == nil {
		t.Error("DialContext is nil")
	}
}

func TestNewUsesBoundedTransport(t *testing.T) {
	c := New()
	if c.Timeout != 0 {
		t.Errorf("Client.Timeout = %v, want 0 (no full-body deadline)", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport type = %T, want *http.Transport", c.Transport)
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 30s", tr.ResponseHeaderTimeout)
	}
}
