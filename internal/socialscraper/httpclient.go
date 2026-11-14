package socialscraper

import (
	"errors"
	"io"
	"net/http"
	"time"
)

// MaxResponseBytes caps every outbound response body.
const MaxResponseBytes = 16 << 20

// ErrBodyTooLarge is returned by reads of a response body over MaxResponseBytes.
var ErrBodyTooLarge = errors.New("response body exceeds 16 MiB")

// NewClient returns an outbound client bounded by timeout whose response bodies are capped at
// MaxResponseBytes. rt nil means a clone of http.DefaultTransport; tests pass a scrapertest router.
func NewClient(timeout time.Duration, rt http.RoundTripper) *http.Client {
	if rt == nil {
		rt = http.DefaultTransport.(*http.Transport).Clone()
	}
	return &http.Client{Timeout: timeout, Transport: capTransport{rt}}
}

type capTransport struct{ next http.RoundTripper }

func (c capTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.next.RoundTrip(r)
	if err == nil {
		resp.Body = &capBody{ReadCloser: resp.Body, left: MaxResponseBytes}
	}
	return resp, err
}

type capBody struct {
	io.ReadCloser
	left int64
}

func (b *capBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, ErrBodyTooLarge
	}
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return n - 1, ErrBodyTooLarge // the extra byte proves the overflow; it is not delivered
	}
	return n, err
}
