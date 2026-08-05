package httpclient

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestDrainErrorBodyNil(t *testing.T) {
	if err := DrainErrorBody(nil); err != nil {
		t.Fatalf("DrainErrorBody(nil) = %v, want nil", err)
	}
}

func TestDrainErrorBodyCapsAtMax(t *testing.T) {
	// Body larger than the cap: only MaxErrorBodyDrain bytes should be read.
	payload := bytes.Repeat([]byte("x"), int(MaxErrorBodyDrain)+4096)
	r := &countingReader{r: bytes.NewReader(payload)}
	if err := DrainErrorBody(r); err != nil {
		t.Fatalf("DrainErrorBody: %v", err)
	}
	if r.n != MaxErrorBodyDrain {
		t.Fatalf("bytes read = %d, want %d", r.n, MaxErrorBodyDrain)
	}
}

func TestDrainErrorBodySmallBody(t *testing.T) {
	const want = "short error page"
	r := &countingReader{r: strings.NewReader(want)}
	if err := DrainErrorBody(r); err != nil {
		t.Fatalf("DrainErrorBody: %v", err)
	}
	if r.n != int64(len(want)) {
		t.Fatalf("bytes read = %d, want %d", r.n, len(want))
	}
}

func TestDrainErrorBodyPropagatesReadError(t *testing.T) {
	boom := errors.New("read failed")
	err := DrainErrorBody(io.MultiReader(strings.NewReader("partial"), errReader{err: boom}))
	if !errors.Is(err, boom) {
		t.Fatalf("DrainErrorBody error = %v, want %v", err, boom)
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type errReader struct {
	err error
}

func (e errReader) Read([]byte) (int, error) {
	return 0, e.err
}
