package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

const mistralKey = "sk-very-secret-key"

// mistralMock is a stand-in for the Mistral chat-completions API.
type mistralMock struct {
	*httptest.Server
	mu    sync.Mutex
	calls int
	meth  string
	hdr   http.Header
	body  map[string]any
}

func newMistral(t *testing.T, reply func(w http.ResponseWriter, r *http.Request)) *mistralMock {
	m := &mistralMock{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.calls, m.meth, m.hdr = m.calls+1, r.Method, r.Header
		m.body = nil
		json.Unmarshal(b, &m.body)
		m.mu.Unlock()
		reply(w, r)
	}))
	t.Cleanup(m.Close)
	return m
}

// chat wraps content into a chat-completions reply.
func chat(content string, extra map[string]any) string {
	o := map[string]any{"model": "mistral-small-2506", "usage": map[string]any{"total_tokens": 42},
		"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}}}
	for k, v := range extra {
		o[k] = v
	}
	b, _ := json.Marshal(o)
	return string(b)
}

func replyWith(code int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code); w.Write([]byte(body)) }
}

const okReply = `{"source_language":"uk","translated_text":"Hello world"}`

func tdef(endpoint string) *registry.Analyzer {
	return &registry.Analyzer{ID: "translation", Execution: registry.Execution{
		Adapter: registry.AdapterMistralTranslation, Endpoint: endpoint, APIKey: mistralKey, Model: "mistral-small-latest",
		Headers: map[string]string{"X-Org": "org1"}}}
}

func treq(text string) Request {
	return Request{JobID: "j1", AnalyzerID: "translation", Inputs: map[string]any{"content": map[string]any{"normalized_content": text}}}
}

func texec(ctx context.Context, d *registry.Analyzer, text string) (json.RawMessage, error) {
	return (&Executor{}).Execute(ctx, d, treq(text))
}

func TestTranslationRequestAndResult(t *testing.T) {
	m := newMistral(t, replyWith(200, chat(okReply, nil)))
	got, err := texec(context.Background(), tdef(m.URL), "Привіт, світе")
	require.NoError(t, err)
	require.JSONEq(t, `{"translated_text":"Hello world","source_language":"uk","model":"mistral-small-2506",
		"truncated":false,"input_characters":13,"usage":{"total_tokens":42}}`, string(got))
	require.Equal(t, 1, m.calls)
	require.Equal(t, "POST", m.meth)
	require.Equal(t, "application/json", m.hdr.Get("Content-Type"))
	require.Equal(t, "application/json", m.hdr.Get("Accept"))
	require.Equal(t, "Bearer "+mistralKey, m.hdr.Get("Authorization"))
	require.Equal(t, "org1", m.hdr.Get("X-Org"))
	require.Empty(t, m.hdr.Get("X-Analysis-Job-Id"))
	require.Empty(t, m.hdr.Get("X-Analysis-Analyzer-Id"))
	require.Equal(t, "mistral-small-latest", m.body["model"])
	require.EqualValues(t, 0, m.body["temperature"])
	require.Equal(t, map[string]any{"type": "json_object"}, m.body["response_format"])
	msgs := m.body["messages"].([]any)
	require.Len(t, msgs, 2)
	sys := msgs[0].(map[string]any)
	require.Equal(t, "system", sys["role"])
	for _, w := range []string{"English", "unchanged", "instructions", "source_language", "translated_text", "ISO 639-1"} {
		require.Contains(t, sys["content"], w)
	}
	require.Equal(t, map[string]any{"role": "user", "content": "Привіт, світе"}, msgs[1])
}

func TestTranslationEnglishAndLanguageNormalization(t *testing.T) {
	m := newMistral(t, replyWith(200, chat(`{"source_language":"en","translated_text":"Same text"}`, nil)))
	got, err := texec(context.Background(), tdef(m.URL), "Same text")
	require.NoError(t, err)
	require.Contains(t, string(got), `"translated_text":"Same text"`)
	require.Contains(t, string(got), `"source_language":"en"`)

	for name, content := range map[string]string{
		"null":    `{"source_language":null,"translated_text":"x"}`,
		"number":  `{"source_language":5,"translated_text":"x"}`,
		"missing": `{"translated_text":"x","extra":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			m := newMistral(t, replyWith(200, chat(content, map[string]any{"model": "", "usage": "n/a"})))
			got, err := texec(context.Background(), tdef(m.URL), "t")
			require.NoError(t, err)
			require.JSONEq(t, `{"translated_text":"x","source_language":null,"model":"mistral-small-latest",
				"truncated":false,"input_characters":1,"usage":null}`, string(got), "response model else configured, unknown members dropped")
		})
	}
}

func TestTranslationTruncation(t *testing.T) {
	cases := []struct {
		name, text string
		limit      int
		sent       string
		truncated  bool
	}{
		{"fits exactly", "один два", 8, "один два", false},
		{"cut at last whitespace", "один два три", 10, "один два", true},
		{"whitespace right at the limit", "один два три", 8, "один два", true},
		{"hard cut without whitespace", "абвгдежзик", 4, "абвг", true},
		{"emoji are single code points", "😀😀😀😀😀😀", 3, "😀😀😀", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newMistral(t, replyWith(200, chat(okReply, nil)))
			d := tdef(m.URL)
			d.Execution.MaxInputChars = &c.limit
			got, err := texec(context.Background(), d, c.text)
			require.NoError(t, err)
			msgs := m.body["messages"].([]any)
			require.Equal(t, c.sent, msgs[1].(map[string]any)["content"])
			var r struct {
				Truncated bool `json:"truncated"`
				Chars     int  `json:"input_characters"`
			}
			require.NoError(t, json.Unmarshal(got, &r))
			require.Equal(t, c.truncated, r.Truncated)
			require.Equal(t, len([]rune(c.text)), r.Chars)
		})
	}
}

func TestTranslationEmptyInputCallsNothing(t *testing.T) {
	m := newMistral(t, replyWith(200, chat(okReply, nil)))
	for _, text := range []string{"", " \n\t"} {
		_, err := texec(context.Background(), tdef(m.URL), text)
		require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
	}
	_, err := (&Executor{}).Execute(context.Background(), tdef(m.URL), Request{Inputs: map[string]any{}})
	require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
	require.Zero(t, m.calls)
}

func TestTranslationFailures(t *testing.T) {
	cases := map[string]struct {
		code int
		body string
	}{
		"401":                  {401, `{"message":"Unauthorized"}`},
		"429":                  {429, `{"message":"rate limited"}`},
		"500":                  {500, `{"message":"boom"}`},
		"non-JSON body":        {200, `<html>oops</html>`},
		"JSON array body":      {200, `[1]`},
		"empty body":           {200, ``},
		"no choices":           {200, `{"choices":[]}`},
		"choices missing":      {200, `{}`},
		"no message":           {200, `{"choices":[{}]}`},
		"content null":         {200, `{"choices":[{"message":{"content":null}}]}`},
		"content not JSON":     {200, chat("Hello world", nil)},
		"content is array":     {200, chat(`["a"]`, nil)},
		"content is string":    {200, chat(`"a"`, nil)},
		"translated missing":   {200, chat(`{"source_language":"uk"}`, nil)},
		"translated empty":     {200, chat(`{"translated_text":""}`, nil)},
		"translated blank":     {200, chat(`{"translated_text":"  \n"}`, nil)},
		"translated not str":   {200, chat(`{"translated_text":5}`, nil)},
		"translated null":      {200, chat(`{"translated_text":null}`, nil)},
		"finish_reason length": {200, `{"choices":[{"finish_reason":"length","message":{"content":"{\"translated_text\":\"cut\"}"}}]}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := newMistral(t, replyWith(c.code, c.body))
			_, err := texec(context.Background(), tdef(m.URL), "text")
			require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
			require.Equal(t, 1, m.calls, "no retries")
			noLeak(t, err.Error(), m.URL, c.body)
		})
	}
}

// noLeak asserts s names neither the key, the Authorization value, the endpoint nor the provider body.
func noLeak(t *testing.T, s, endpoint, providerBody string) {
	t.Helper()
	require.NotContains(t, s, mistralKey)
	require.NotContains(t, s, "Bearer")
	require.NotContains(t, s, endpoint)
	if len(providerBody) > 8 {
		require.NotContains(t, s, providerBody)
	}
	require.NotContains(t, s, "Unauthorized")
	require.NotContains(t, s, "rate limited")
	require.NotContains(t, s, "Hello world")
}

func TestTranslationEmptyAPIKey(t *testing.T) {
	m := newMistral(t, replyWith(200, chat(okReply, nil)))
	d := tdef(m.URL)
	d.Execution.APIKey = ""
	_, err := texec(context.Background(), d, "text")
	require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
	require.Zero(t, m.calls)
}

func TestTranslationRedirectAndConnectionError(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	for _, code := range []int{301, 302, 307, 308} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, code)
		}))
		_, err := texec(context.Background(), tdef(srv.URL), "t")
		srv.Close()
		require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
		noLeak(t, err.Error(), srv.URL, "")
		require.NotContains(t, err.Error(), target.URL)
	}
	require.Zero(t, hits.Load(), "the Authorization header never reaches the redirect target")

	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := texec(context.Background(), tdef(url), "t")
	require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
	noLeak(t, err.Error(), url, "")
}

func TestTranslationTimeoutAndCancel(t *testing.T) {
	m := newMistral(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := texec(ctx, tdef(m.URL), "t")
	require.Equal(t, job.CodeTimeout, runErr(t, err).Code)
	noLeak(t, err.Error(), m.URL, "")

	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	_, err = texec(ctx, tdef(m.URL), "t")
	require.Equal(t, job.CodeCancelled, runErr(t, err).Code)
	require.Less(t, time.Since(start), 2*time.Second)
	noLeak(t, err.Error(), m.URL, "")
}

func TestTranslationHTTPStatusMayBeNamed(t *testing.T) {
	m := newMistral(t, replyWith(429, `{"message":"rate limited"}`))
	_, err := texec(context.Background(), tdef(m.URL), "t")
	require.Contains(t, strings.ToLower(err.Error()), "429")
}
