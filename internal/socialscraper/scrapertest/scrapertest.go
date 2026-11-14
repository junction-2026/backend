// Package scrapertest gives module tests an offline HTTP client: requests are routed to per-host
// handlers in memory and a request to a host without a route fails the test.
package scrapertest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Router is an http.RoundTripper over per-host handlers.
type Router struct {
	t        testing.TB
	mu       sync.Mutex
	handlers map[string]http.Handler
	errs     map[string]error
	requests []*http.Request
}

// New returns an empty router; every request fails t until a host is routed.
func New(t testing.TB) *Router {
	return &Router{t: t, handlers: map[string]http.Handler{}, errs: map[string]error{}}
}

// Handle routes host (as in req.URL.Host, so with an explicit port when the URL has one) to h.
func (r *Router) Handle(host string, h http.Handler) *Router {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[host] = h
	return r
}

// Fail makes every request to host fail at the transport level with err.
func (r *Router) Fail(host string, err error) *Router {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs[host] = err
	return r
}

// Client returns an http.Client over the router. It follows redirects like http.DefaultClient.
func (r *Router) Client() *http.Client { return &http.Client{Transport: r} }

// Requests returns the requests seen so far (routed or not).
func (r *Router) Requests() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*http.Request(nil), r.requests...)
}

// RoundTrip implements http.RoundTripper. The handler runs in its own goroutine so a context
// deadline ends the request even while the handler blocks.
func (r *Router) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	h, ok := r.handlers[req.URL.Host]
	err := r.errs[req.URL.Host]
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !ok {
		r.t.Errorf("scrapertest: request to unrouted host %q (%s): tests must not reach a real platform", req.URL.Host, req.URL)
		return nil, &unroutedError{req.URL.Host}
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	rec := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		defer close(served)
		in := req.Clone(req.Context())
		in.RequestURI = req.URL.RequestURI()
		h.ServeHTTP(rec, in)
	}()
	select {
	case <-served:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

type unroutedError struct{ host string }

func (e *unroutedError) Error() string { return "scrapertest: unrouted host " + e.host }

// Fixture reads testdata/<name> of the calling package's directory.
func Fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

// Body serves a fixed JSON body with the given status and extra headers (key, value, ...).
func Body(status int, body []byte, headers ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i+1 < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}
