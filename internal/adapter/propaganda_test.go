package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// keywordMock is a stand-in for the PropagandaDetection service.
type keywordMock struct {
	*httptest.Server
	mu    sync.Mutex
	calls int
	body  []byte
	hdr   http.Header
	meth  string
}

func newKeywordMock(t *testing.T) *keywordMock {
	m := &keywordMock{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Text string }
		var raw json.RawMessage
		json.NewDecoder(r.Body).Decode(&raw)
		json.Unmarshal(raw, &in)
		m.mu.Lock()
		m.calls, m.body, m.hdr, m.meth = m.calls+1, raw, r.Header, r.Method
		m.mu.Unlock()
		label, score := "NO_PROPAGANDA", 0.8
		if lt := strings.ToLower(in.Text); strings.Contains(lt, "propaganda") || strings.Contains(lt, "fake news") {
			label, score = "PROPAGANDA", 0.9
		}
		json.NewEncoder(w).Encode(map[string]any{"result": []any{map[string]any{"label": label, "score": score}}, "latency_ms": 12.3})
	}))
	t.Cleanup(m.Close)
	return m
}

func pdef(endpoint string) *registry.Analyzer {
	d := def(registry.AdapterPropagandaDetection, endpoint)
	d.ID = "propaganda_detection"
	return d
}

func preq(text string) Request {
	return Request{JobID: "j1", AnalyzerID: "propaganda_detection", Inputs: map[string]any{"content": map[string]any{
		"content": "<p>" + text + "</p>", "normalized_content": text, "content_type": "text/html", "source": "s"}}}
}

func pexec(ctx context.Context, d *registry.Analyzer, text string) (json.RawMessage, error) {
	return (&Executor{}).Execute(ctx, d, preq(text))
}

func TestPropagandaRequestAndVerbatimResult(t *testing.T) {
	m := newKeywordMock(t)
	got, err := pexec(context.Background(), pdef(m.URL), "This is FAKE News!")
	require.NoError(t, err)
	require.JSONEq(t, `{"result":[{"label":"PROPAGANDA","score":0.9}],"latency_ms":12.3}`, string(got))
	require.Equal(t, 1, m.calls)
	require.Equal(t, "POST", m.meth)
	require.Equal(t, `{"text":"This is FAKE News!"}`, string(m.body))
	require.Equal(t, "application/json", m.hdr.Get("Content-Type"))
	require.Equal(t, "application/json", m.hdr.Get("Accept"))
	require.Equal(t, "j1", m.hdr.Get("X-Analysis-Job-Id"))
	require.Equal(t, "propaganda_detection", m.hdr.Get("X-Analysis-Analyzer-Id"))
	require.Equal(t, "Bearer tok", m.hdr.Get("Authorization"))

	got, err = pexec(context.Background(), pdef(m.URL), "weather is nice")
	require.NoError(t, err)
	require.JSONEq(t, `{"result":[{"label":"NO_PROPAGANDA","score":0.8}],"latency_ms":12.3}`, string(got))
}

func TestPropagandaKeepsUnknownMembers(t *testing.T) {
	body := `{"result":[{"label":"NO_PROPAGANDA","score":0,"extra":1},{"label":"PROPAGANDA","score":1}],"model":"x"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer srv.Close()
	got, err := pexec(context.Background(), pdef(srv.URL), "t")
	require.NoError(t, err)
	require.Equal(t, body, string(got))
}

func TestPropagandaInvalidResponses(t *testing.T) {
	ok := `{"label":"PROPAGANDA","score":0.5}`
	cases := map[string]struct {
		code int
		body string
	}{
		"not json":            {200, `{oops`},
		"array":               {200, `[1]`},
		"empty":               {200, ``},
		"no result":           {200, `{"latency_ms":1}`},
		"result null":         {200, `{"result":null}`},
		"result not array":    {200, `{"result":{"label":"PROPAGANDA","score":1}}`},
		"result empty":        {200, `{"result":[]}`},
		"element string":      {200, `{"result":["x"]}`},
		"element null":        {200, `{"result":[null]}`},
		"one bad element":     {200, `{"result":[` + ok + `,5]}`},
		"label missing":       {200, `{"result":[{"score":0.5}]}`},
		"label unknown":       {200, `{"result":[{"label":"MAYBE","score":0.5}]}`},
		"label lowercase":     {200, `{"result":[{"label":"propaganda","score":0.5}]}`},
		"label not string":    {200, `{"result":[{"label":1,"score":0.5}]}`},
		"score missing":       {200, `{"result":[{"label":"PROPAGANDA"}]}`},
		"score string":        {200, `{"result":[{"label":"PROPAGANDA","score":"0.5"}]}`},
		"score null":          {200, `{"result":[{"label":"PROPAGANDA","score":null}]}`},
		"score above 1":       {200, `{"result":[{"label":"PROPAGANDA","score":1.01}]}`},
		"score below 0":       {200, `{"result":[{"label":"PROPAGANDA","score":-0.1}]}`},
		"latency string":      {200, `{"result":[` + ok + `],"latency_ms":"12"}`},
		"latency null":        {200, `{"result":[` + ok + `],"latency_ms":null}`},
		"latency negative":    {200, `{"result":[` + ok + `],"latency_ms":-1}`},
		"non-2xx with valid":  {500, `{"result":[` + ok + `]}`},
		"client error":        {400, `{}`},
		"service unavailable": {503, ``},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.code)
				w.Write([]byte(c.body))
			}))
			defer srv.Close()
			_, err := pexec(context.Background(), pdef(srv.URL), "t")
			require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
			require.NotContains(t, err.Error(), srv.URL)
			require.NotContains(t, err.Error(), "tok")
		})
	}
}

func TestPropagandaRedirectNotFollowed(t *testing.T) {
	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	for _, code := range []int{301, 302, 307, 308} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, code)
		}))
		_, err := pexec(context.Background(), pdef(srv.URL), "t")
		srv.Close()
		require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
		require.NotContains(t, err.Error(), target.URL)
	}
	require.Zero(t, hits)
}

func TestPropagandaConnectionErrorAndNoRetry(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := pexec(context.Background(), pdef(url), "t")
	require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
	require.NotContains(t, err.Error(), url)

	var calls int
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(502) }))
	defer srv.Close()
	_, err = pexec(context.Background(), pdef(srv.URL), "t")
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func TestPropagandaTimeoutAndCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := pexec(ctx, pdef(srv.URL), "t")
	require.Equal(t, job.CodeTimeout, runErr(t, err).Code)

	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	_, err = pexec(ctx, pdef(srv.URL), "t")
	require.Equal(t, job.CodeCancelled, runErr(t, err).Code)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestPropagandaMissingText(t *testing.T) {
	_, err := (&Executor{}).Execute(context.Background(), pdef("http://127.0.0.1:1"), Request{Inputs: map[string]any{}})
	require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
}
