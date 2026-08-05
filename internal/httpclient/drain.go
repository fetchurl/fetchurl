package httpclient

import (
	"io"
)

// MaxErrorBodyDrain is how many bytes of a non-OK response body we will read
// before closing. Enough to free the connection for keep-alive reuse without
// pinning multi-GB error pages in memory or stalling multi-request loops
// (Fetcher fallback, seed URL walks, similar outbound clients).
const MaxErrorBodyDrain int64 = 32 << 10 // 32 KiB

// DrainErrorBody discards up to MaxErrorBodyDrain bytes from body so HTTP/1.x
// keep-alive can recycle the connection after a non-OK response. Close alone
// does not guarantee reuse when the body was not fully consumed.
//
// A nil body is a no-op. The returned error is from the read, if any.
func DrainErrorBody(body io.Reader) error {
	if body == nil {
		return nil
	}
	_, err := io.Copy(io.Discard, io.LimitReader(body, MaxErrorBodyDrain))
	return err
}
