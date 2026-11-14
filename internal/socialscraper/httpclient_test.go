package socialscraper

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func bodyOf(n int) http.RoundTripper {
	return rtFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("a", n))), Request: r}, nil
	})
}

func TestClientCapsBodies(t *testing.T) {
	get := func(rt http.RoundTripper) ([]byte, error) {
		resp, err := NewClient(time.Second, rt).Get("https://example.test/")
		require.NoError(t, err)
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}
	b, err := get(bodyOf(MaxResponseBytes))
	require.NoError(t, err)
	assert.Len(t, b, MaxResponseBytes)

	_, err = get(bodyOf(MaxResponseBytes + 1))
	assert.ErrorIs(t, err, ErrBodyTooLarge)
}

func TestClientHasTimeout(t *testing.T) {
	c := NewClient(1500*time.Millisecond, bodyOf(1))
	assert.Equal(t, 1500*time.Millisecond, c.Timeout)
	assert.NotNil(t, NewClient(time.Second, nil).Transport)

	slow := rtFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	_, err := NewClient(20*time.Millisecond, slow).Get("https://example.test/")
	require.Error(t, err)
	assert.True(t, IsTimeout(err))
	assert.False(t, IsTimeout(errors.New("other")))
}
