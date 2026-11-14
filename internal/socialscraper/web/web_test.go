package web

import (
	"context"
	"encoding/json"
	"errors"
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

const (
	sentinelKey = "oxy-key-SENTINEL-9f3a"
	articleURL  = "https://www.dw.com/en/election-results/a-70000001"
)

// stubProvider is the Oxylabs endpoint: every request is recorded, answers come from respond.
type stubProvider struct {
	mu      sync.Mutex
	bodies  []map[string]any
	headers []http.Header
	respond func(n int, body map[string]any) (int, []byte)
}

func (p *stubProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	p.mu.Lock()
	p.bodies = append(p.bodies, body)
	p.headers = append(p.headers, r.Header.Clone())
	n := len(p.bodies)
	p.mu.Unlock()
	status, out := p.respond(n, body)
	scrapertest.Body(status, out)(w, r)
}

func (p *stubProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.bodies)
}

func result(html, markdown string, extra map[string]any) []byte {
	r := map[string]any{"html": html, "markdown": markdown}
	for k, v := range extra {
		r[k] = v
	}
	b, _ := json.Marshal(map[string]any{"results": []any{r}})
	return b
}

func ok(html, md string) func(int, map[string]any) (int, []byte) {
	return func(int, map[string]any) (int, []byte) { return 200, result(html, md, nil) }
}

type fixtures struct{ article, shell, login, empty, md string }

func load(t *testing.T) fixtures {
	return fixtures{string(scrapertest.Fixture(t, "news_article.html")), string(scrapertest.Fixture(t, "js_shell.html")),
		string(scrapertest.Fixture(t, "login_wall.html")), string(scrapertest.Fixture(t, "empty.html")),
		string(scrapertest.Fixture(t, "markdown_only.md"))}
}

func webAPI() ss.WebConfig {
	return ss.WebConfig{API: ss.APIWeb, WebAPIKey: sentinelKey, MinTextChars: 300, CacheTTL: time.Hour, Timeout: 5 * time.Second}
}

// setup returns a module over a stub provider routed by host, so the default Oxylabs URLs are exercised.
func setup(t *testing.T, cfg ss.WebConfig, respond func(int, map[string]any) (int, []byte)) (*Module, *stubProvider) {
	p := &stubProvider{respond: respond}
	host := "webapi.oxylabs.io"
	if cfg.API == ss.APIWebScraper {
		host = "realtime.oxylabs.io"
	}
	m := New(cfg, 5*time.Second, ss.NewClient(0, scrapertest.New(t).Handle(host, p)))
	m.retryDelay = 0
	return m, p
}

func scrape(m *Module, raw string) (*ss.Result, error) {
	t, rerr := ss.Recognize(raw)
	if rerr != nil {
		return nil, rerr
	}
	return m.Scrape(context.Background(), t, ss.Request{})
}

func code(t *testing.T, err error) *ss.Error {
	t.Helper()
	var se *ss.Error
	require.True(t, errors.As(err, &se), "typed error expected, got %v", err)
	assert.Equal(t, "web", se.Details["platform"])
	assert.NotContains(t, se.Error(), sentinelKey)
	b, _ := json.Marshal(se.Details)
	assert.NotContains(t, string(b), sentinelKey)
	return se
}

func TestArticle(t *testing.T) {
	f := load(t)
	m, p := setup(t, webAPI(), ok(f.article, ""))
	res, err := scrape(m, articleURL+"?utm_source=x#top")
	require.NoError(t, err)

	// request builder
	require.Equal(t, 1, p.count())
	b := p.bodies[0]
	assert.Equal(t, articleURL+"?utm_source=x", b["url"], "query kept, fragment dropped")
	assert.Equal(t, []any{"html", "markdown"}, b["output"])
	assert.Equal(t, false, b["run_js"])
	assert.Equal(t, "desktop", b["device"])
	assert.NotContains(t, b, "location")
	assert.Equal(t, "Bearer "+sentinelKey, p.headers[0].Get("Authorization"))

	assert.Equal(t, "web", res.Platform)
	assert.Equal(t, "page", res.URLKind)
	pg := res.Page
	assert.Contains(t, pg.Text, "Voters in the capital chose a new parliament")
	assert.Contains(t, pg.Text, `"People wanted change, and they said so clearly,"`) // entities decoded
	assert.Contains(t, pg.Text, "\n")
	for _, boilerplate := range []string{"Home World Business", "© 2026 Example Media", "Privacy", "Great article", "Markets react"} {
		assert.NotContains(t, pg.Text, boilerplate)
	}
	assert.Equal(t, "Election results", *pg.Title)
	assert.Equal(t, "DW", *pg.SiteName)
	assert.Equal(t, "Jane Doe", *pg.Author)
	assert.Equal(t, "2026-11-13T07:30:00Z", *pg.PublishedAt)
	assert.Equal(t, "en", *pg.Lang)
	assert.Equal(t, "https://www.dw.com/en/election-results/a-70000001", *pg.CanonicalURL)
	assert.Equal(t, "Voters in the capital chose a new parliament on Sunday.", *pg.Description)
	assert.Equal(t, "dw.com", pg.Host)
	assert.Equal(t, "dw.com", *pg.Domain)
	assert.Equal(t, articleURL+"?utm_source=x", pg.RequestedURL)
	assert.Equal(t, pg.RequestedURL, pg.FinalURL)
	assert.Equal(t, len(strings.Fields(pg.Text)), pg.WordCount)
	assert.False(t, pg.Truncated)
	assert.Equal(t, "trafilatura", pg.Extraction)
	assert.False(t, pg.Rendered)
	assert.False(t, pg.Cached)
	assert.Equal(t, "oxylabs", pg.Provider)
	assert.Equal(t, 1, pg.OxylabsRequests)
}

func TestMetadataPrecedenceAndFallbacks(t *testing.T) {
	page := func(head string) *docInfo { return parseDoc("<html><head>" + head + "</head><body></body></html>") }
	m := page(`<title>Tag title</title><meta property="og:title" content="OG title">
		<meta name="author" content="Meta Author"><meta property="article:author" content="Art Author">
		<meta property="og:locale" content="de_DE"><meta property="og:url" content="https://example.org/og">
		<meta property="article:published_time" content="yesterday"><meta name="description" content="plain">`).metadata()
	assert.Equal(t, "OG title", *m.title)
	assert.Equal(t, "Meta Author", *m.author)
	assert.Equal(t, "de-DE", *m.lang, "og:locale underscore to hyphen")
	assert.Equal(t, "https://example.org/og", *m.canonical)
	assert.Nil(t, m.publishedAt, "unparseable date")
	assert.Equal(t, "plain", *m.description)
	assert.Nil(t, m.siteName)

	m = page(`<title>Tag title</title><link rel="canonical" href="/relative"><meta property="article:author" content="https://facebook.com/x">
		<script type="application/ld+json">{"@type":"Article","publisher":{"name":"Pub"},"author":"Solo Author"}</script>`).metadata()
	assert.Equal(t, "Tag title", *m.title)
	assert.Equal(t, "Pub", *m.siteName)
	assert.Equal(t, "Solo Author", *m.author)
	assert.Nil(t, m.canonical, "relative canonical is not absolute http(s)")

	m = page(`<meta property="article:published_time" content="2026-11-13T08:30:00+0100">
		<script type="application/ld+json">{"not json</script>`).metadata()
	assert.Equal(t, "2026-11-13T07:30:00Z", *m.publishedAt)
	assert.Nil(t, m.title)
	assert.Nil(t, m.lang)
}

func TestHostAndDomain(t *testing.T) {
	f := load(t)
	for _, c := range []struct{ url, host, domain string }{
		{"https://www.bbc.co.uk/news/articles/abc", "bbc.co.uk", "bbc.co.uk"},
		{"https://news.bbc.co.uk/a", "news.bbc.co.uk", "bbc.co.uk"},
		{"https://WWW.Example.ORG./a", "example.org", "example.org"},
	} {
		m, _ := setup(t, webAPI(), ok(f.article, ""))
		res, err := scrape(m, c.url)
		require.NoError(t, err, c.url)
		assert.Equal(t, c.host, res.Host, c.url)
		assert.Equal(t, c.domain, *res.Domain, c.url)
	}
	m, _ := setup(t, webAPI(), ok(f.article, ""))
	res, err := scrape(m, "https://8.8.8.8/page")
	require.NoError(t, err)
	assert.Equal(t, "8.8.8.8", res.Host)
	assert.Nil(t, res.Domain, "IP host has no registered domain")
}

func TestFinalURLFromProvider(t *testing.T) {
	f := load(t)
	m, _ := setup(t, webAPI(), func(int, map[string]any) (int, []byte) {
		return 200, result(f.article, "", map[string]any{"url": "https://www.dw.com/en/moved/a-1", "status_code": 200})
	})
	res, err := scrape(m, "https://dw.com/old")
	require.NoError(t, err)
	assert.Equal(t, "https://dw.com/old", res.RequestedURL)
	assert.Equal(t, "https://www.dw.com/en/moved/a-1", res.FinalURL)
	assert.Equal(t, "dw.com", res.Host)
}

func TestRenderingRetry(t *testing.T) {
	f := load(t)
	m, p := setup(t, webAPI(), func(_ int, b map[string]any) (int, []byte) {
		if b["run_js"] == true {
			return 200, result(f.article, "", nil)
		}
		return 200, result(f.shell, "", nil)
	})
	res, err := scrape(m, articleURL)
	require.NoError(t, err)
	require.Equal(t, 2, p.count())
	assert.Equal(t, false, p.bodies[0]["run_js"])
	assert.Equal(t, true, p.bodies[1]["run_js"])
	assert.True(t, res.Rendered)
	assert.Equal(t, 2, res.OxylabsRequests)
	assert.Contains(t, res.Text, "Voters in the capital")
	assert.Equal(t, "trafilatura", res.Extraction)
}

func TestMarkdownFallback(t *testing.T) {
	f := load(t)
	m, p := setup(t, webAPI(), ok(f.shell, f.md))
	res, err := scrape(m, "https://news.example.org/harbour")
	require.NoError(t, err)
	assert.Equal(t, 2, p.count(), "rendered once, still no HTML text")
	assert.Equal(t, "markdown_fallback", res.Extraction)
	for _, syntax := range []string{"#", "**", "](", "```", "![", "`", "> ", "- ", "http"} {
		assert.NotContains(t, res.Text, syntax)
	}
	assert.Contains(t, res.Text, "The city harbour reopened on Tuesday after eleven months of dredging work, officials said. Ships of up to nine metres")
	assert.Contains(t, res.Text, "See the port authority statement for details.")
	assert.Contains(t, res.Text, "Harbour reopens after dredging\n")
	assert.Contains(t, res.Text, "Pilot fees fall by 12% for night arrivals\n")
}

func TestLoginWallAndEmptyAndShortPage(t *testing.T) {
	f := load(t)
	m, p := setup(t, webAPI(), ok(f.login, ""))
	_, err := scrape(m, "https://www.instagram.com/nasa/")
	se := code(t, err)
	assert.Equal(t, "login_required", se.Code)
	assert.NotContains(t, se.Details, "missing_settings")
	assert.Equal(t, 2, p.count(), "unrendered then rendered")

	m, _ = setup(t, webAPI(), ok(f.login, ""))
	_, err = scrape(m, "https://example.org/login/sso")
	assert.Equal(t, "login_required", code(t, err).Code, "login path below /login")

	m, _ = setup(t, webAPI(), ok(f.empty, ""))
	_, err = scrape(m, "https://example.org/blank")
	assert.Equal(t, "no_text_content", code(t, err).Code)

	short := "<html><body><p>" + strings.Repeat("word ", 24) + "</p></body></html>" // 120 characters, no marker
	m, _ = setup(t, webAPI(), ok(short, ""))
	res, err := scrape(m, "https://example.org/short")
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(strings.Repeat("word ", 24)), res.Text)
}

func TestLongTextTruncated(t *testing.T) {
	var paras []string
	for i := 0; i < 35; i++ {
		paras = append(paras, "<p>"+strings.Repeat("lorem ipsum dolor ", 60)+"</p>") // ~1,080 chars each, 37,800 in all
	}
	m, _ := setup(t, webAPI(), ok("<html><body><article>"+strings.Join(paras, "")+"</article></body></html>", ""))
	res, err := scrape(m, "https://example.org/long")
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.LessOrEqual(t, len([]rune(res.Text)), 20000)
	assert.Greater(t, len([]rune(res.Text)), 19900)
	assert.Regexp(t, `(lorem|ipsum|dolor)$`, res.Text, "cut at a word boundary")

	cut, tr := capText(strings.Repeat("é", 20000) + " tail")
	assert.True(t, tr)
	assert.Equal(t, strings.Repeat("é", 20000), cut)
	// no usable whitespace boundary: hard cut instead of emptying the text
	for _, in := range []string{strings.Repeat("字", 25000), strings.Repeat("字", 100) + " " + strings.Repeat("字", 25000)} {
		cut, tr = capText(in)
		assert.True(t, tr)
		assert.Len(t, []rune(cut), 20000)
	}
	cut, tr = capText(strings.Repeat("é", 100))
	assert.False(t, tr)
	assert.Len(t, []rune(cut), 100)
}

func TestNotConfigured(t *testing.T) {
	cfg := webAPI()
	cfg.WebAPIKey = ""
	m, p := setup(t, cfg, ok("", ""))
	_, err := scrape(m, articleURL)
	se := code(t, err)
	assert.Equal(t, "platform_not_configured", se.Code)
	assert.Equal(t, []string{"OXYLABS_WEB_API_KEY"}, se.Details["missing_settings"])
	assert.Zero(t, p.count())

	cfg = ss.WebConfig{API: ss.APIWebScraper, Username: "u"}
	m, p = setup(t, cfg, ok("", ""))
	_, err = scrape(m, articleURL)
	assert.Equal(t, []string{"OXYLABS_PASSWORD"}, code(t, err).Details["missing_settings"])
	assert.Zero(t, p.count())
}

func TestProviderFailures(t *testing.T) {
	f := load(t)
	status := func(s int, extra map[string]any) func(int, map[string]any) (int, []byte) {
		return func(int, map[string]any) (int, []byte) { return s, result(f.article, "", extra) }
	}
	for name, c := range map[string]struct {
		respond  func(int, map[string]any) (int, []byte)
		code     string
		requests int
		target   any
	}{
		"target 403": {status(200, map[string]any{"status_code": 403}), "upstream_error", 1, 403},
		"target 404": {status(200, map[string]any{"status_code": 404}), "upstream_error", 1, 404},
		"key rejected": {func(int, map[string]any) (int, []byte) {
			return 401, []byte(`{"message":"bad key ` + sentinelKey + `"}`)
		}, "upstream_error", 1, nil},
		"bad request":     {func(int, map[string]any) (int, []byte) { return 400, []byte(`{}`) }, "upstream_error", 1, nil},
		"unreadable":      {func(int, map[string]any) (int, []byte) { return 200, []byte(`<html>`) }, "upstream_error", 1, nil},
		"no results":      {func(int, map[string]any) (int, []byte) { return 200, []byte(`{"results":[]}`) }, "upstream_error", 1, nil},
		"5xx twice":       {func(int, map[string]any) (int, []byte) { return 503, []byte(`{}`) }, "upstream_error", 2, nil},
		"429 twice":       {func(int, map[string]any) (int, []byte) { return 429, []byte(`{}`) }, "rate_limited", 2, nil},
		"403 not retried": {func(int, map[string]any) (int, []byte) { return 403, nil }, "upstream_error", 1, nil},
	} {
		t.Run(name, func(t *testing.T) {
			m, p := setup(t, webAPI(), c.respond)
			_, err := scrape(m, articleURL)
			se := code(t, err)
			assert.Equal(t, c.code, se.Code)
			assert.Equal(t, c.requests, p.count())
			assert.Equal(t, c.target, se.Details["target_status"])
			if c.target == 403 {
				assert.Contains(t, se.Message, "refused")
			}
		})
	}

	t.Run("429 with Retry-After", func(t *testing.T) {
		p := &stubProvider{respond: func(int, map[string]any) (int, []byte) { return 429, []byte(`{}`) }}
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "17")
			p.ServeHTTP(w, r)
		})
		m := New(webAPI(), time.Second, ss.NewClient(0, scrapertest.New(t).Handle("webapi.oxylabs.io", h)))
		m.retryDelay = 0
		_, err := scrape(m, articleURL)
		assert.Equal(t, 17, code(t, err).Details["retry_after_s"])
	})
}

func TestRetryOnceThenSucceed(t *testing.T) {
	f := load(t)
	for _, first := range []int{429, 500, 502} {
		m, p := setup(t, webAPI(), func(n int, _ map[string]any) (int, []byte) {
			if n == 1 {
				return first, []byte(`{}`)
			}
			return 200, result(f.article, "", nil)
		})
		res, err := scrape(m, articleURL)
		require.NoError(t, err, first)
		assert.Equal(t, 2, p.count())
		assert.Equal(t, 2, res.OxylabsRequests)
		assert.False(t, res.Rendered)
	}
}

func TestAtMostThreeRequests(t *testing.T) {
	f := load(t)
	// unrendered 503, retry gives a shell, rendered fetch 503: the retry budget is spent, so no 4th request
	m, p := setup(t, webAPI(), func(n int, _ map[string]any) (int, []byte) {
		if n == 1 || n == 3 {
			return 503, []byte(`{}`)
		}
		return 200, result(f.shell, "", nil)
	})
	_, err := scrape(m, articleURL)
	assert.Equal(t, "upstream_error", code(t, err).Code)
	assert.Equal(t, 3, p.count())

	m, p = setup(t, webAPI(), func(n int, _ map[string]any) (int, []byte) {
		if n == 1 {
			return 503, []byte(`{}`)
		}
		return 200, result(f.shell, f.md, nil)
	})
	res, err := scrape(m, articleURL)
	require.NoError(t, err)
	assert.Equal(t, 3, p.count())
	assert.Equal(t, 3, res.OxylabsRequests)
	assert.Equal(t, "markdown_fallback", res.Extraction)
}

func TestTimeout(t *testing.T) {
	cfg := webAPI()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	m := New(cfg, 30*time.Millisecond, ss.NewClient(0, scrapertest.New(t).Handle("webapi.oxylabs.io", h)))
	_, err := scrape(m, articleURL)
	assert.Equal(t, "upstream_timeout", code(t, err).Code)

	m = New(cfg, 30*time.Millisecond, ss.NewClient(0, scrapertest.New(t).Fail("webapi.oxylabs.io", errors.New("dial tcp: connection refused to "+sentinelKey))))
	_, err = scrape(m, articleURL)
	se := code(t, err)
	assert.Equal(t, "upstream_error", se.Code)
}

func TestCache(t *testing.T) {
	f := load(t)
	m, p := setup(t, webAPI(), ok(f.article, ""))
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }

	first, err := scrape(m, articleURL)
	require.NoError(t, err)
	assert.False(t, first.Cached)
	now = now.Add(30 * time.Minute)
	// equivalent spellings share one cache entry
	second, err := scrape(m, "HTTPS://WWW.DW.COM:443/en/election-results/a-70000001#frag")
	require.NoError(t, err)
	assert.Equal(t, 1, p.count())
	assert.True(t, second.Cached)
	assert.Equal(t, 0, second.OxylabsRequests)
	assert.Equal(t, first.Text, second.Text)
	assert.False(t, first.Cached, "the first result is not mutated")
	assert.Equal(t, 1, first.OxylabsRequests)

	_, err = scrape(m, articleURL+"?page=2") // the query is part of the key
	require.NoError(t, err)
	assert.Equal(t, 2, p.count())

	now = now.Add(31 * time.Minute) // past the 1 h lifetime
	third, err := scrape(m, articleURL)
	require.NoError(t, err)
	assert.False(t, third.Cached)
	assert.Equal(t, 3, p.count())
}

func TestFailuresAreNotCached(t *testing.T) {
	f := load(t)
	m, p := setup(t, webAPI(), func(n int, _ map[string]any) (int, []byte) {
		if n == 1 {
			return 200, result(f.article, "", map[string]any{"status_code": 500})
		}
		return 200, result(f.article, "", nil)
	})
	_, err := scrape(m, articleURL)
	require.Error(t, err)
	res, err := scrape(m, articleURL)
	require.NoError(t, err)
	assert.Equal(t, 2, p.count())
	assert.False(t, res.Cached)
}

func TestCacheEvictsOldest(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	c := newCache(time.Hour, func() time.Time { return now })
	for i := 0; i < maxCacheEntries+10; i++ {
		now = now.Add(time.Second)
		c.put("k"+string(rune('a'+i%26))+strings.Repeat("x", i), &ss.Result{Page: &ss.Page{Text: "t"}})
	}
	assert.Len(t, c.m, maxCacheEntries)
	_, ok := c.get("ka")
	assert.False(t, ok, "the oldest entry was evicted")
	_, ok = c.get("k" + string(rune('a'+(maxCacheEntries+9)%26)) + strings.Repeat("x", maxCacheEntries+9))
	assert.True(t, ok)
}

func TestWebScraperAPI(t *testing.T) {
	f := load(t)
	cfg := ss.WebConfig{API: ss.APIWebScraper, Username: "user-1", Password: sentinelKey, Location: "DE", MinTextChars: 300,
		CacheTTL: time.Hour, Timeout: 5 * time.Second}
	m, p := setup(t, cfg, func(_ int, b map[string]any) (int, []byte) {
		out := f.shell
		if _, rendered := b["render"]; rendered {
			out = f.article
		}
		body, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"content": out, "status_code": 200, "url": articleURL}}})
		return 200, body
	})
	res, err := scrape(m, articleURL)
	require.NoError(t, err)
	require.Equal(t, 2, p.count())
	first, second := p.bodies[0], p.bodies[1]
	assert.Equal(t, map[string]any{"source": "universal", "url": articleURL, "markdown": true, "geo_location": "DE"}, first)
	assert.Equal(t, "html", second["render"])
	user, pass, hasBasic := (&http.Request{Header: p.headers[0]}).BasicAuth()
	assert.True(t, hasBasic)
	assert.Equal(t, "user-1", user)
	assert.Equal(t, sentinelKey, pass)
	assert.Empty(t, p.headers[0].Get("Bearer"))
	assert.True(t, res.Rendered)
	assert.Equal(t, "Election results", *res.Title)
	assert.Contains(t, res.Text, "Voters in the capital")

	// Markdown content (markdown:true) maps to the same result through the Markdown fallback
	m, _ = setup(t, cfg, func(int, map[string]any) (int, []byte) {
		body, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"content": f.md}}})
		return 200, body
	})
	res, err = scrape(m, "https://news.example.org/harbour")
	require.NoError(t, err)
	assert.Equal(t, "markdown_fallback", res.Extraction)
}

func TestWebAPILocationAndOverride(t *testing.T) {
	f := load(t)
	cfg := webAPI()
	cfg.Location = "US"
	p := &stubProvider{respond: ok(f.article, "")}
	m := New(cfg, time.Second, ss.NewClient(0, scrapertest.New(t).Handle("stub.test", p)))
	m.WebAPIURL = "https://stub.test/v1/scrape"
	_, err := scrape(m, articleURL)
	require.NoError(t, err)
	assert.Equal(t, "US", p.bodies[0]["location"])
}

func TestProviderRedirectNotFollowed(t *testing.T) {
	router := scrapertest.New(t).Handle("webapi.oxylabs.io", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.test/", http.StatusTemporaryRedirect)
	}))
	m := New(webAPI(), time.Second, router.Client())
	_, err := scrape(m, articleURL)
	assert.Equal(t, "upstream_error", code(t, err).Code)
	for _, r := range router.Requests() {
		assert.NotEqual(t, "evil.test", r.URL.Host, "the Authorization header must not follow a redirect")
	}
}

func TestStripMarkdown(t *testing.T) {
	for in, want := range map[string]string{
		"# Title\n\nbody **bold** and _em_ text":                    "Title\nbody bold and em text",
		"- one\n- two\n3. three":                                    "one\ntwo\nthree",
		"a [label](https://x.test/a_b_c) b ![alt](https://i/x.png)": "a label b",
		"snake_case_name stays":                                     "snake_case_name stays",
		"```go\ncode line\n```\n---\nafter":                         "code line\nafter",
		"line one\nline two\n\nnext":                                "line one line two\nnext",
		"> quote\n> more":                                           "quote more",
	} {
		assert.Equal(t, want, stripMarkdown(in), in)
	}
}

func TestRequestKey(t *testing.T) {
	same := []string{"https://Example.com/a?x=1", "HTTPS://EXAMPLE.COM:443/a?x=1#f", "https://example.com./a?x=1"}
	var want string
	for i, raw := range same {
		tg, err := ss.Recognize(raw)
		require.Nil(t, err)
		if i == 0 {
			want = requestKey(tg.URL)
		}
		assert.Equal(t, want, requestKey(tg.URL), raw)
	}
	a, _ := ss.Recognize("https://example.com:8443/a")
	b, _ := ss.Recognize("https://example.com/a")
	assert.NotEqual(t, requestKey(a.URL), requestKey(b.URL))
}
