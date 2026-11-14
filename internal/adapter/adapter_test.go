package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

func def(adapter, endpoint string) *registry.Analyzer {
	return &registry.Analyzer{ID: "sentiment", Execution: registry.Execution{
		Adapter: adapter, Endpoint: endpoint, Method: "POST", PollInterval: 5 * time.Millisecond,
		Headers: map[string]string{"Authorization": "Bearer tok"}}}
}

func req() Request {
	return Request{JobID: "j1", AnalyzerID: "sentiment", Inputs: map[string]any{"content": "hi"}}
}

func runErr(t *testing.T, err error) *job.RunError {
	t.Helper()
	var re *job.RunError
	require.True(t, errors.As(err, &re), "got %v", err)
	return re
}

func exec(ctx context.Context, d *registry.Analyzer) (json.RawMessage, error) {
	return (&Executor{Handlers: NewHandlers()}).Execute(ctx, d, req())
}

func TestHTTPSyncSuccessAndRequestShape(t *testing.T) {
	var gotBody map[string]any
	var gotHdr http.Header
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHdr, method = r.Header, r.Method
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"score": 12}`))
	}))
	defer srv.Close()
	got, err := exec(context.Background(), def(registry.AdapterHTTPSync, srv.URL))
	require.NoError(t, err)
	require.JSONEq(t, `{"score":12}`, string(got))
	require.Equal(t, "POST", method)
	require.Equal(t, map[string]any{"job_id": "j1", "analyzer_id": "sentiment", "inputs": map[string]any{"content": "hi"}}, gotBody)
	require.Equal(t, "application/json", gotHdr.Get("Content-Type"))
	require.Equal(t, "application/json", gotHdr.Get("Accept"))
	require.Equal(t, "j1", gotHdr.Get("X-Analysis-Job-Id"))
	require.Equal(t, "sentiment", gotHdr.Get("X-Analysis-Analyzer-Id"))
	require.Equal(t, "Bearer tok", gotHdr.Get("Authorization"))
}

func TestHTTPSyncFailures(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"5xx":        func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) },
		"4xx":        func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400); w.Write([]byte(`{}`)) },
		"array body": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[1]`)) },
		"string":     func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`"x"`)) },
		"not json":   func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{oops`)) },
		"empty":      func(w http.ResponseWriter, r *http.Request) {},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			_, err := exec(context.Background(), def(registry.AdapterHTTPSync, srv.URL))
			require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
			require.NotContains(t, err.Error(), srv.URL, "messages never carry endpoint URLs")
		})
	}
	// connection error
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := exec(context.Background(), def(registry.AdapterHTTPSync, url))
	require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
	require.NotContains(t, err.Error(), url)
}

func TestHTTPSyncTimeoutAndCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // lets the server notice the client going away
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := exec(ctx, def(registry.AdapterHTTPSync, srv.URL))
	require.Equal(t, job.CodeTimeout, runErr(t, err).Code)

	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	_, err = exec(ctx, def(registry.AdapterHTTPSync, srv.URL))
	require.Equal(t, job.CodeCancelled, runErr(t, err).Code)
	require.Less(t, time.Since(start), 2*time.Second)
}

// asyncServer implements the service side of the polling protocol.
type asyncServer struct {
	*httptest.Server
	polls   atomic.Int32
	mu      sync.Mutex
	deletes int
	delHdr  http.Header
	poll    func(n int) (int, string)
}

func newAsync(t *testing.T, poll func(n int) (int, string)) *asyncServer {
	s := &asyncServer{poll: poll}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /submit", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		w.Write([]byte(`{"status_url": "` + s.URL + `/tasks/1"}`))
	})
	mux.HandleFunc("GET /tasks/1", func(w http.ResponseWriter, r *http.Request) {
		code, body := s.poll(int(s.polls.Add(1)))
		w.WriteHeader(code)
		w.Write([]byte(body))
	})
	mux.HandleFunc("DELETE /tasks/1", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.deletes++
		s.delHdr = r.Header
		s.mu.Unlock()
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *asyncServer) Deletes() int { s.mu.Lock(); defer s.mu.Unlock(); return s.deletes }

func TestHTTPAsyncSuccess(t *testing.T) {
	s := newAsync(t, func(n int) (int, string) {
		switch n {
		case 1:
			return 200, `{"status":"pending"}`
		case 2:
			return 200, `{"status":"somethingnew"}` // unknown counts as running
		}
		return 200, `{"status":"succeeded","result":{"score":12}}`
	})
	got, err := exec(context.Background(), def(registry.AdapterHTTPAsync, s.URL+"/submit"))
	require.NoError(t, err)
	require.JSONEq(t, `{"score":12}`, string(got))
	require.EqualValues(t, 3, s.polls.Load())
	require.Zero(t, s.Deletes())
}

func TestHTTPAsyncImmediateResultAndSubmitFailures(t *testing.T) {
	cases := map[string]struct {
		code int
		body string
		ok   bool
	}{
		"immediate 200":     {200, `{"score": 1}`, true},
		"202 empty object":  {202, `{}`, false},
		"202 relative url":  {202, `{"status_url":"/tasks/1"}`, false},
		"202 non-http url":  {202, `{"status_url":"ftp://x/1"}`, false},
		"202 not an object": {202, `[]`, false},
		"201":               {201, `{"status_url":"http://x/1"}`, false},
		"500":               {500, ``, false},
		"200 not an object": {200, `"x"`, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(c.code); w.Write([]byte(c.body)) }))
			defer srv.Close()
			_, err := exec(context.Background(), def(registry.AdapterHTTPAsync, srv.URL))
			if c.ok {
				require.NoError(t, err)
				return
			}
			require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
		})
	}
}

func TestHTTPAsyncPollFailures(t *testing.T) {
	cases := map[string]struct {
		code    int
		body    string
		message string
	}{
		"poll 500":            {500, ``, "HTTP 500"},
		"poll 404":            {404, `{"status":"running"}`, "HTTP 404"},
		"failed with msg":     {200, `{"status":"failed","error":{"message":"model crashed"}}`, "model crashed"},
		"failed no msg":       {200, `{"status":"failed"}`, "reported failure"},
		"succeeded no result": {200, `{"status":"succeeded"}`, "result"},
		"poll not object":     {200, `[]`, "JSON object"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newAsync(t, func(int) (int, string) { return c.code, c.body })
			_, err := exec(context.Background(), def(registry.AdapterHTTPAsync, s.URL+"/submit"))
			re := runErr(t, err)
			require.Equal(t, job.CodeAdapterError, re.Code)
			require.Contains(t, re.Message, c.message)
			require.Zero(t, s.Deletes(), "no DELETE when the service itself ended the task")
		})
	}
}

func TestHTTPAsyncDeleteOnTimeoutAndCancel(t *testing.T) {
	for name, code := range map[string]string{"timeout": job.CodeTimeout, "cancel": job.CodeCancelled} {
		t.Run(name, func(t *testing.T) {
			s := newAsync(t, func(int) (int, string) { return 200, `{"status":"running"}` })
			var ctx context.Context
			var cancel context.CancelFunc
			if name == "timeout" {
				ctx, cancel = context.WithTimeout(context.Background(), 60*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
				time.AfterFunc(60*time.Millisecond, cancel)
			}
			defer cancel()
			start := time.Now()
			_, err := exec(ctx, def(registry.AdapterHTTPAsync, s.URL+"/submit"))
			require.Equal(t, code, runErr(t, err).Code)
			require.Less(t, time.Since(start), 2*time.Second)
			require.Equal(t, 1, s.Deletes(), "exactly one best-effort DELETE")
			require.Equal(t, "j1", s.delHdr.Get("X-Analysis-Job-Id"))
			require.Equal(t, "Bearer tok", s.delHdr.Get("Authorization"))
		})
	}
}

func TestHTTPAsyncDoesNotSendCredentialsToOtherHosts(t *testing.T) {
	var auth atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		w.Write([]byte(`{"status":"succeeded","result":{"ok":true}}`))
	}))
	defer other.Close()
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		w.Write([]byte(`{"status_url":"` + other.URL + `/t"}`))
	}))
	defer svc.Close()
	_, err := exec(context.Background(), def(registry.AdapterHTTPAsync, svc.URL))
	require.NoError(t, err)
	require.Equal(t, "", auth.Load())
}

func TestCustomAdapter(t *testing.T) {
	h := NewHandlers()
	h.Register("echo", func(_ context.Context, r Request) (any, error) { return map[string]any{"id": r.AnalyzerID}, nil })
	h.Register("list", func(context.Context, Request) (any, error) { return []int{1}, nil })
	h.Register("err", func(context.Context, Request) (any, error) { return nil, errors.New("nope") })
	h.Register("panic", func(context.Context, Request) (any, error) { panic("boom") })
	h.Register("typed", func(context.Context, Request) (any, error) {
		return nil, &job.RunError{Code: job.CodeInvalidOutput, Message: "bad"}
	})
	h.Register("stuck", func(context.Context, Request) (any, error) { select {} })
	x := &Executor{Handlers: h}
	d := func(name string) *registry.Analyzer {
		return &registry.Analyzer{ID: "a", Execution: registry.Execution{Adapter: registry.AdapterCustom, Handler: name}}
	}

	got, err := x.Execute(context.Background(), d("echo"), req())
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"sentiment"}`, string(got))
	_, err = x.Execute(context.Background(), d("list"), req())
	require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
	_, err = x.Execute(context.Background(), d("err"), req())
	require.Equal(t, "nope", runErr(t, err).Message)
	_, err = x.Execute(context.Background(), d("panic"), req())
	require.Contains(t, runErr(t, err).Message, "panicked")
	_, err = x.Execute(context.Background(), d("typed"), req())
	require.Equal(t, job.CodeInvalidOutput, runErr(t, err).Code)
	_, err = x.Execute(context.Background(), d("missing"), req())
	require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)

	// a handler that ignores its context cannot hold the adapter beyond cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = x.Execute(ctx, d("stuck"), req())
	require.Equal(t, job.CodeTimeout, runErr(t, err).Code)
	require.Less(t, time.Since(start), time.Second)
}

func TestTextStatsAndInputs(t *testing.T) {
	j := &job.Job{ID: "j1", Input: job.Input{Content: "A  b\r\nc", ContentType: "text/plain", NormalizedContent: "A  b\nc",
		Source: job.Source{Title: "t"}},
		Analyzers: []job.AnalyzerRun{{ID: "dep", Status: job.RunSucceeded, Result: json.RawMessage(`{"items":[]}`)}, {ID: "x", Status: job.RunFailed}}}

	in := Inputs(&registry.Analyzer{}, j)
	b, _ := json.Marshal(in)
	require.JSONEq(t, `{"content":{"content":"A  b\r\nc","normalized_content":"A  b\nc","content_type":"text/plain","source":{"title":"t"}}}`, string(b))

	d := &registry.Analyzer{Trigger: registry.TriggerAfterAll, Input: registry.Input{Bindings: map[string]string{
		"c": "$input", "s": "$input.source", "id": "$job.id", "dep": "$analyzers.dep", "all": "$job.analyzers", "cov": "$job.coverage"}}}
	in = Inputs(d, j)
	b, _ = json.Marshal(in)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	require.JSONEq(t, `{"title":"t"}`, string(m["s"]))
	require.JSONEq(t, `"j1"`, string(m["id"]))
	require.JSONEq(t, `{"items":[]}`, string(m["dep"]))
	require.Contains(t, string(m["all"]), `"id":"dep"`)
	require.Contains(t, string(m["cov"]), `"settled":2`)

	got, err := textStats(context.Background(), Request{Inputs: Inputs(&registry.Analyzer{}, j)})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"characters": 6, "words": 3, "lines": 2}, got)
	got, _ = textStats(context.Background(), Request{Inputs: map[string]any{"x": ""}})
	require.Equal(t, map[string]int{"characters": 0, "words": 0, "lines": 0}, got)
}
