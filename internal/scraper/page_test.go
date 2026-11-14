package scraper

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/outlets"
	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/scrapertest"
	"github.com/junction-2026/backend/internal/socialscraper/web"
)

func webFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "socialscraper", "web", "testdata", name))
	require.NoError(t, err)
	return string(b)
}

const key = "SENTINEL-oxy-4d2c91ab"

func pageResult(canonical, final, domain *string) []byte {
	b, _ := json.Marshal(ss.Result{Platform: "web", URLKind: "page", Posts: []ss.Post{}, Comments: []ss.Comment{}, Warnings: []ss.Warning{},
		Page: &ss.Page{Text: "First paragraph.\nSecond paragraph.", CanonicalURL: canonical, FinalURL: *final, Domain: domain,
			Title: ss.Ptr("Election results"), Author: ss.Ptr("Jane Doe"), PublishedAt: ss.Ptr("2026-11-13T07:30:00Z")}})
	return b
}

func TestPageMapping(t *testing.T) {
	requested := "https://www.dw.com/en/some-article/a-12345678?utm_source=x"
	final := ss.Ptr("https://www.dw.com/en/some-article/a-12345678?utm_source=x")
	canon := ss.Ptr("https://www.dw.com/en/some-article/a-12345678")

	res, err := mapResult(requested, pageResult(canon, final, ss.Ptr("dw.com")))
	require.NoError(t, err)
	in := res.Input
	require.Equal(t, "First paragraph.\nSecond paragraph.", in.Content)
	require.Equal(t, "text/plain", in.ContentType)
	require.Equal(t, job.Source{URL: *canon, Title: "Election results", Author: "Jane Doe", Platform: "web",
		PublishedAt: job.Ptr(time.Date(2026, 11, 13, 7, 30, 0, 0, time.UTC)), Metadata: in.Source.Metadata}, in.Source)
	require.Equal(t, "page", res.URLKind)
	var meta struct{ Scrape map[string]any }
	require.NoError(t, json.Unmarshal(in.Source.Metadata, &meta))
	require.Equal(t, "Election results", meta.Scrape["title"])

	// a canonical URL of another registered domain is ignored (it must not borrow another outlet's record)
	copycat := ss.Ptr("https://copycat.example/story")
	res, err = mapResult("https://www.dw.com/story", pageResult(copycat, ss.Ptr("https://www.dw.com/story"), ss.Ptr("dw.com")))
	require.NoError(t, err)
	require.Equal(t, "https://www.dw.com/story", res.Input.Source.URL)
	res, err = mapResult("https://copycat.example/story", pageResult(canon, ss.Ptr("https://copycat.example/story"), ss.Ptr("copycat.example")))
	require.NoError(t, err)
	require.Equal(t, "https://copycat.example/story", res.Input.Source.URL)

	// without canonical URL, domain or metadata: the final URL, nothing absent invented
	raw, _ := json.Marshal(map[string]any{"platform": "web", "url_kind": "page", "text": "Body.", "final_url": "https://example.org/a"})
	res, err = mapResult(requested, raw)
	require.NoError(t, err)
	require.Equal(t, job.Source{URL: "https://example.org/a", Platform: "web", Metadata: res.Input.Source.Metadata}, res.Input.Source)

	// blank text is no_text_content; a missing or non-string text is not a valid ScrapeResult
	_, err = mapResult(requested, []byte(`{"platform":"web","url_kind":"page","text":"  ","final_url":"x"}`))
	require.Equal(t, job.CodeNoTextContent, err.(*job.ScrapeError).Code)
	for _, bad := range []string{`{"platform":"web","url_kind":"page"}`, `{"platform":"web","url_kind":"page","text":5}`, `{"platform":"web","url_kind":"page","text":null}`} {
		_, err = mapResult(requested, []byte(bad))
		require.Equal(t, job.CodeScraperUnavailable, err.(*job.ScrapeError).Code, bad)
	}
}

// pageClient runs the real core and web module against a stub Oxylabs serving html.
func pageClient(t *testing.T, cfg ss.WebConfig, html string) *Client {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"html": html, "markdown": ""}}})
	router := scrapertest.New(t).Handle("webapi.oxylabs.io", scrapertest.Body(200, body))
	reg := ss.NewRegistry()
	reg.Register(ss.Web, web.New(cfg, 5*time.Second, ss.NewClient(0, router)))
	return &Client{Core: ss.New(reg, nil), Redactor: ss.NewRedactor(func(string) string { return "" }, key)}
}

func webCfg(key string) ss.WebConfig {
	return ss.WebConfig{API: ss.APIWeb, WebAPIKey: key, MinTextChars: 300, CacheTTL: time.Hour, Timeout: 5 * time.Second}
}

func TestArticleJobInputAndOutlet(t *testing.T) {
	html := webFixture(t, "news_article.html")
	c := pageClient(t, webCfg(key), html)
	res, err := c.Scrape(context.Background(), req("https://www.dw.com/en/election-results/a-70000001?utm_source=x"))
	require.NoError(t, err)
	in := res.Input
	require.Equal(t, "web", res.Platform)
	require.Contains(t, in.Content, "The governing coalition lost its majority")
	require.NotContains(t, in.Content, "Home World Business")
	require.NotContains(t, in.Content, "© 2026 Example Media")
	require.Equal(t, "https://www.dw.com/en/election-results/a-70000001", in.Source.URL, "canonical URL on the same domain")
	require.Equal(t, "Election results", in.Source.Title)
	require.Equal(t, "Jane Doe", in.Source.Author)
	require.Equal(t, time.Date(2026, 11, 13, 7, 30, 0, 0, time.UTC), *in.Source.PublishedAt)
	require.NotContains(t, string(in.Source.Metadata), key)

	table, lerr := outlets.Load("../../data/outlets.csv")
	require.NoError(t, lerr)
	o, _ := table.Match(outlets.Host(in.Source.URL))
	require.NotNil(t, o)
	require.Equal(t, "Deutsche Welle", o.Name)
	require.Equal(t, "DE", o.CountryCode)
	require.Equal(t, "center", o.Bias)
}

func TestPageErrorsPassThrough(t *testing.T) {
	// the missing key is named, never its value; no provider request is made (an unrouted request would fail the test)
	c := pageClient(t, webCfg(""), "")
	se := scrapeErr(t, c, context.Background(), "https://x.com/alice/status/1790000000000000000")
	require.Equal(t, "platform_not_configured", se.Code)
	require.JSONEq(t, `{"platform":"web","missing_settings":["OXYLABS_WEB_API_KEY"]}`, string(se.Details))

	c = pageClient(t, webCfg(key), webFixture(t, "login_wall.html"))
	se = scrapeErr(t, c, context.Background(), "https://www.instagram.com/nasa/")
	require.Equal(t, "login_required", se.Code)
	require.JSONEq(t, `{"platform":"web"}`, string(se.Details))
	require.False(t, strings.Contains(se.Message, key))

	c = pageClient(t, webCfg(key), webFixture(t, "empty.html"))
	require.Equal(t, "no_text_content", scrapeErr(t, c, context.Background(), "https://example.org/blank").Code)
}
