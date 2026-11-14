package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/scrapertest"
)

const aiHost = "api-aistudio.oxylabs.io"

// aiRun scripts one run, selected by its output_format.
type aiRun struct {
	processing int    // polls answered "processing" before the final status
	status     string // final status, default "completed"
	errCode    string // error_code of a failed poll
	data       any    // data member of the data answer
	never      bool   // always "processing"
	startCode  int    // non-2xx answer of POST /scrape
	startBody  string
}

type aiRec struct {
	method, path string
	query        string
	header       http.Header
	body         map[string]any
}

// aiStub is AI Studio: it records every request and answers per run kind.
type aiStub struct {
	mu    sync.Mutex
	reqs  []aiRec
	runs  map[string]aiRun // "json" | "markdown"
	polls map[string]int
}

func (s *aiStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, aiRec{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), body})
	out := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	id := strings.TrimPrefix(r.URL.Query().Get("run_id"), "run-")
	run := s.runs[id]
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/scrape":
		kind, _ := body["output_format"].(string)
		run = s.runs[kind]
		if run.startCode != 0 {
			w.WriteHeader(run.startCode)
			_, _ = io.WriteString(w, run.startBody)
			return
		}
		out(200, map[string]any{"run_id": "run-" + kind})
	case r.URL.Path == "/scrape/run":
		s.polls[id]++
		st := run.status
		if st == "" {
			st = "completed"
		}
		if run.never || s.polls[id] <= run.processing {
			st = "processing"
		}
		resp := map[string]any{"run_id": "run-" + id, "status": st}
		if run.errCode != "" {
			resp["error_code"] = run.errCode
		}
		out(200, resp)
	case r.URL.Path == "/scrape/run/data":
		out(200, map[string]any{"run_id": "run-" + id, "status": "completed", "data": run.data})
	default:
		out(404, map[string]any{})
	}
}

func (s *aiStub) count(method, path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.reqs {
		if r.method == method && r.path == path {
			n++
		}
	}
	return n
}

func (s *aiStub) started() int { return s.count(http.MethodPost, "/scrape") }

func aiCfg() ss.WebConfig {
	return ss.WebConfig{API: ss.APIAIStudio, AIStudioKey: sentinelKey, MinTextChars: 300, CacheTTL: time.Hour, Timeout: 5 * time.Second}
}

func aiSetup(t *testing.T, cfg ss.WebConfig, runs map[string]aiRun) (*Module, *aiStub) {
	s := &aiStub{runs: runs, polls: map[string]int{}}
	m := New(cfg, 5*time.Second, ss.NewClient(0, scrapertest.New(t).Handle(aiHost, s)))
	m.pollStart, m.pollMax = time.Millisecond, 3*time.Millisecond
	return m, s
}

func jsonData(kind, body string, extra map[string]any) map[string]any {
	d := map[string]any{"page_kind": kind, "body": body}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

var longBody = strings.Repeat("The election results were announced on Sunday evening. ", 20)

func TestAIStudioJSONPath(t *testing.T) {
	data := jsonData("article", longBody, map[string]any{"title": "Results", "site_name": "DW", "author": "Jane Doe",
		"description": "Summary", "canonical_url": "https://www.dw.com/en/results", "published_at": "2026-03-01T10:00:00+02:00", "lang": "EN"})
	m, s := aiSetup(t, aiCfg(), map[string]aiRun{"json": {processing: 1, data: data}})
	res, err := scrape(m, articleURL+"#top")
	require.NoError(t, err)
	p := res.Page
	assert.Equal(t, strings.TrimSpace(longBody), p.Text)
	assert.Equal(t, "Results", *p.Title)
	assert.Equal(t, "DW", *p.SiteName)
	assert.Equal(t, "Jane Doe", *p.Author)
	assert.Equal(t, "Summary", *p.Description)
	assert.Equal(t, "https://www.dw.com/en/results", *p.CanonicalURL)
	assert.Equal(t, "2026-03-01T08:00:00Z", *p.PublishedAt)
	assert.Equal(t, "en", *p.Lang)
	assert.Equal(t, "ai_studio_json", p.Extraction)
	assert.Equal(t, "oxylabs_ai_studio", p.Provider)
	assert.True(t, p.Rendered)
	assert.False(t, p.Cached)
	assert.Equal(t, 1, p.OxylabsRequests)
	assert.Equal(t, "dw.com", *p.Domain)
	assert.Equal(t, "www.dw.com"[4:], p.Host)
	assert.Equal(t, articleURL, p.FinalURL)
	assert.Equal(t, len(strings.Fields(p.Text)), p.WordCount)

	// request protocol: start, two polls (processing, completed), one data fetch
	assert.Equal(t, 1, s.started())
	assert.Equal(t, 2, s.count(http.MethodGet, "/scrape/run"))
	assert.Equal(t, 1, s.count(http.MethodGet, "/scrape/run/data"))
	start := s.reqs[0]
	assert.Equal(t, sentinelKey, start.header.Get("x-api-key"))
	assert.Empty(t, start.header.Get("Authorization"))
	assert.Equal(t, "application/json", start.header.Get("Content-Type"))
	assert.True(t, strings.HasPrefix(start.header.Get("User-Agent"), "junction-backend/"))
	assert.Equal(t, "json", start.body["output_format"])
	assert.Equal(t, true, start.body["render_javascript"])
	assert.Equal(t, articleURL, start.body["url"])
	props := start.body["openapi_schema"].(map[string]any)["properties"].(map[string]any)
	for _, k := range []string{"title", "site_name", "author", "published_at", "lang", "canonical_url", "description", "body", "page_kind"} {
		assert.Contains(t, props, k)
	}
	assert.Equal(t, "run_id=run-json", s.reqs[1].query)
	assert.Equal(t, "run_id=run-json", s.reqs[3].query)
}

func TestAIStudioMarkdownFallback(t *testing.T) {
	md := "# Heading\n\n" + strings.Repeat("Some **bold** text with a [link](https://x.example) inside. ", 20)
	m, s := aiSetup(t, aiCfg(), map[string]aiRun{
		"json":     {data: jsonData("article", strings.Repeat("x", 50), map[string]any{"title": "Kept"})},
		"markdown": {data: md}})
	res, err := scrape(m, articleURL)
	require.NoError(t, err)
	assert.Equal(t, "markdown_fallback", res.Page.Extraction)
	assert.Equal(t, 2, res.Page.OxylabsRequests)
	assert.True(t, res.Page.Rendered)
	assert.Equal(t, "Kept", *res.Page.Title, "metadata of the JSON run is kept")
	assert.NotContains(t, res.Page.Text, "**")
	assert.NotContains(t, res.Page.Text, "https://x.example")
	assert.Equal(t, "markdown", s.reqs[3].body["output_format"])
	assert.NotContains(t, s.reqs[3].body, "openapi_schema")
	assert.Equal(t, true, s.reqs[3].body["render_javascript"])
}

func TestAIStudioMarkdownFallbackAfterFailedJSON(t *testing.T) {
	m, s := aiSetup(t, aiCfg(), map[string]aiRun{
		"json": {status: "failed", errCode: "target_unreachable"}, "markdown": {data: strings.Repeat("Plain text. ", 60)}})
	res, err := scrape(m, articleURL)
	require.NoError(t, err)
	assert.Equal(t, "markdown_fallback", res.Page.Extraction)
	assert.Nil(t, res.Page.Title)
	assert.Equal(t, 2, s.started())
}

func TestAIStudioShortBodyKeptWhenMarkdownHasNoMore(t *testing.T) {
	m, _ := aiSetup(t, aiCfg(), map[string]aiRun{"json": {data: jsonData("post", "A short post.", nil)}, "markdown": {data: ""}})
	res, err := scrape(m, "https://example.com/post/1")
	require.NoError(t, err)
	assert.Equal(t, "A short post.", res.Page.Text)
	assert.Equal(t, "ai_studio_json", res.Page.Extraction)
	assert.Equal(t, 2, res.Page.OxylabsRequests)
}

func TestAIStudioErrors(t *testing.T) {
	cases := []struct {
		name string
		runs map[string]aiRun
		code string
		runN int
	}{
		{"login wall", map[string]aiRun{"json": {data: jsonData("login_wall", "Please log in", nil)}}, "login_required", 1},
		{"error page", map[string]aiRun{"json": {data: jsonData("error_page", "", nil)}}, "no_text_content", 1},
		{"empty body", map[string]aiRun{"json": {data: jsonData("article", "", nil)}, "markdown": {data: ""}}, "no_text_content", 2},
		{"short paywall", map[string]aiRun{"json": {data: jsonData("consent_or_paywall", strings.Repeat("x", 100), nil)}}, "login_required", 1},
		{"both runs failed", map[string]aiRun{"json": {status: "failed", errCode: "target_unreachable"},
			"markdown": {status: "failed", errCode: "target_unreachable"}}, "upstream_error", 2},
		{"start rejected", map[string]aiRun{"json": {startCode: 500}, "markdown": {startCode: 500}}, "upstream_error", 2},
		{"auth rejected", map[string]aiRun{"json": {startCode: 401, startBody: `{"error_code":"unauthorized"}`}}, "upstream_error", 1},
		{"forbidden", map[string]aiRun{"json": {startCode: 403}}, "upstream_error", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, s := aiSetup(t, aiCfg(), c.runs)
			_, err := scrape(m, articleURL)
			se := code(t, err)
			assert.Equal(t, c.code, se.Code)
			assert.Equal(t, c.runN, s.started(), "runs started")
			assert.NotContains(t, se.Details, "missing_settings")
			if c.name == "short paywall" {
				assert.Contains(t, se.Message, "consent or paywall")
			}
			switch c.name {
			case "both runs failed":
				assert.Equal(t, "target_unreachable", se.Details["provider_error_code"])
			case "auth rejected":
				assert.Equal(t, "unauthorized", se.Details["provider_error_code"])
			}
		})
	}
}

func TestAIStudioUnknownOrMissingPageKindIsContent(t *testing.T) {
	for _, d := range []map[string]any{{"body": longBody}, {"body": longBody, "page_kind": "weird"}, {"body": longBody, "page_kind": 7}} {
		m, _ := aiSetup(t, aiCfg(), map[string]aiRun{"json": {data: d}})
		res, err := scrape(m, articleURL)
		require.NoError(t, err)
		assert.Equal(t, "ai_studio_json", res.Page.Extraction)
	}
}

func TestAIStudioLongPaywallBodyIsContent(t *testing.T) {
	m, _ := aiSetup(t, aiCfg(), map[string]aiRun{"json": {data: jsonData("consent_or_paywall", longBody, nil)}})
	res, err := scrape(m, articleURL)
	require.NoError(t, err)
	assert.Equal(t, "ai_studio_json", res.Page.Extraction)
}

func TestAIStudioTimeout(t *testing.T) {
	cfg := aiCfg()
	cfg.Timeout = 60 * time.Millisecond
	m, s := aiSetup(t, cfg, map[string]aiRun{"json": {never: true}, "markdown": {never: true}})
	start := time.Now()
	_, err := scrape(m, articleURL)
	assert.Equal(t, "upstream_timeout", code(t, err).Code)
	assert.Equal(t, 2, s.started(), "each run is bounded by the timeout; no third run")
	assert.Less(t, time.Since(start), 3*time.Second)
}

func TestAIStudioCancellation(t *testing.T) {
	m, s := aiSetup(t, aiCfg(), map[string]aiRun{"json": {never: true}, "markdown": {never: true}})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	tgt, rerr := ss.Recognize(articleURL)
	require.Nil(t, rerr)
	start := time.Now()
	_, err := m.Scrape(ctx, tgt, ss.Request{})
	require.Error(t, err)
	assert.Less(t, time.Since(start), time.Second)
	assert.Equal(t, 1, s.started(), "no Markdown run after cancellation")
	time.Sleep(20 * time.Millisecond) // a request in flight at cancellation may still be recorded
	n := s.count(http.MethodGet, "/scrape/run")
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, n, s.count(http.MethodGet, "/scrape/run"), "polling stopped")
}

func TestAIStudioNotConfigured(t *testing.T) {
	cfg := aiCfg()
	cfg.AIStudioKey = ""
	m, s := aiSetup(t, cfg, nil)
	_, err := scrape(m, articleURL)
	se := code(t, err)
	assert.Equal(t, "platform_not_configured", se.Code)
	assert.Equal(t, []string{"OXYLABS_AI_STUDIO_API_KEY"}, se.Details["missing_settings"])
	assert.Empty(t, s.reqs)
}

func TestAIStudioCache(t *testing.T) {
	m, s := aiSetup(t, aiCfg(), map[string]aiRun{"json": {data: jsonData("article", longBody, nil)}})
	_, err := scrape(m, articleURL+"#a")
	require.NoError(t, err)
	res, err := scrape(m, articleURL+"#b")
	require.NoError(t, err)
	assert.True(t, res.Page.Cached)
	assert.Equal(t, 0, res.Page.OxylabsRequests)
	assert.Equal(t, "oxylabs_ai_studio", res.Page.Provider)
	assert.Equal(t, 1, s.started())

	// failures are not cached
	m, s = aiSetup(t, aiCfg(), map[string]aiRun{"json": {status: "failed"}, "markdown": {status: "failed"}})
	for range 2 {
		_, err = scrape(m, articleURL)
		require.Error(t, err)
	}
	assert.Equal(t, 4, s.started())
}

func TestAIStudioSanitizesModelOutput(t *testing.T) {
	dirty := "Te\u200bxt\u200c with\u200d ctrl\x00\x07\u2060\ufeff chars\nsecond\tline"
	long := strings.Repeat("é", 600)
	data := jsonData("article", dirty+" "+longBody, map[string]any{
		"title": "  Ti\u200btle\x01  ", "site_name": long, "author": 42, "description": long,
		"published_at": "yesterday", "lang": "eng", "canonical_url": "javascript:alert(1)"})
	m, _ := aiSetup(t, aiCfg(), map[string]aiRun{"json": {data: data}})
	res, err := scrape(m, articleURL)
	require.NoError(t, err)
	p := res.Page
	assert.True(t, strings.HasPrefix(p.Text, "Text with ctrl chars\nsecond\tline"), p.Text[:40])
	assert.NotContains(t, p.Text, "\u200b")
	assert.Equal(t, "Title", *p.Title)
	assert.Equal(t, 500, len([]rune(*p.SiteName)))
	assert.Equal(t, 500, len([]rune(*p.Description)))
	assert.Nil(t, p.Author, "non-string ignored")
	assert.Nil(t, p.PublishedAt)
	assert.Nil(t, p.Lang)
	assert.Nil(t, p.CanonicalURL)
}

func TestAIStudioTextCap(t *testing.T) {
	m, _ := aiSetup(t, aiCfg(), map[string]aiRun{"json": {data: jsonData("article", strings.Repeat("word ", 6000), nil)}})
	res, err := scrape(m, articleURL)
	require.NoError(t, err)
	assert.True(t, res.Page.Truncated)
	assert.LessOrEqual(t, chars(res.Page.Text), maxTextChars)
}

// A provider that echoes the key in its errors must not get it into errors, details or the result.
func TestAIStudioKeyNeverLeaks(t *testing.T) {
	echo := `{"error_code":"` + sentinelKey + `","message":"bad key ` + sentinelKey + `"}`
	for name, runs := range map[string]map[string]aiRun{
		"start":  {"json": {startCode: 400, startBody: echo}, "markdown": {startCode: 400, startBody: echo}},
		"auth":   {"json": {startCode: 401, startBody: echo}},
		"failed": {"json": {status: "failed", errCode: sentinelKey}, "markdown": {status: "failed", errCode: sentinelKey}},
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := aiSetup(t, aiCfg(), runs)
			_, err := scrape(m, articleURL)
			se := code(t, err) // asserts the key is neither in Error() nor in Details
			assert.NotContains(t, se.Details, "provider_error_code")
		})
	}
	m, _ := aiSetup(t, aiCfg(), map[string]aiRun{"json": {data: jsonData("article", longBody, nil)}})
	res, err := scrape(m, articleURL)
	require.NoError(t, err)
	b, _ := json.Marshal(res)
	assert.NotContains(t, string(b), sentinelKey)
}

func TestAIStudioURLOverride(t *testing.T) {
	cfg := aiCfg()
	cfg.AIStudioURL = "https://aistudio.internal.example:8443/"
	s := &aiStub{runs: map[string]aiRun{"json": {data: jsonData("article", longBody, nil)}}, polls: map[string]int{}}
	m := New(cfg, 5*time.Second, ss.NewClient(0, scrapertest.New(t).Handle("aistudio.internal.example:8443", s)))
	m.pollStart = time.Millisecond
	_, err := scrape(m, articleURL)
	require.NoError(t, err)
	assert.Equal(t, 1, s.started())
}
