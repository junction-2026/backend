package scraper

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const secret = "SENTINEL-ct0-8a7e3b5d"

type moduleFunc func(ctx context.Context, t ss.Target, req ss.Request) (*ss.Result, error)

func (f moduleFunc) Scrape(ctx context.Context, t ss.Target, req ss.Request) (*ss.Result, error) {
	return f(ctx, t, req)
}

// client runs mod for every platform through the real core.
func client(mod ss.Module) *Client {
	reg := ss.NewRegistry()
	for _, p := range []string{ss.Bluesky, ss.Mastodon, ss.Web} {
		reg.Register(p, mod)
	}
	red := ss.NewRedactor(func(k string) string { return map[string]string{"SCRAPER_TOKEN": secret}[k] })
	return &Client{Core: ss.New(reg, nil), Redactor: red}
}

func req(url string) job.ScrapeRequest {
	return job.ScrapeRequest{URL: url, MaxPosts: 20, MaxComments: 50}
}

func scrapeErr(t *testing.T, c *Client, ctx context.Context, url string) *job.ScrapeError {
	t.Helper()
	res, err := c.Scrape(ctx, req(url))
	require.Nil(t, res)
	var se *job.ScrapeError
	require.True(t, errors.As(err, &se), "%v", err)
	require.NotContains(t, se.Message, secret)
	return se
}

func TestErrorMapping(t *testing.T) {
	ret := func(res *ss.Result, err error) moduleFunc {
		return func(context.Context, ss.Target, ss.Request) (*ss.Result, error) { return res, err }
	}
	for name, c := range map[string]struct {
		mod     moduleFunc
		url     string
		code    string
		details string
	}{
		"account_private passes through": {mod: ret(nil, ss.AccountPrivate("mastodon", "private")), code: "account_private", details: `{"platform":"mastodon"}`},
		"rate_limited keeps retry_after": {mod: ret(nil, ss.RateLimited("", 30)), code: "rate_limited", details: `{"platform":"mastodon","retry_after_s":30}`},
		"not configured lists names":     {mod: ret(nil, ss.PlatformNotConfigured("mastodon", "EXAMPLE_SETTING")), code: "platform_not_configured", details: `{"platform":"mastodon","missing_settings":["EXAMPLE_SETTING"]}`},
		"unknown code passes through":    {mod: ret(nil, &ss.Error{Code: "new_code"}), code: "new_code", details: `{"platform":"mastodon"}`},
		"message redacted":               {mod: ret(nil, ss.UpstreamError("mastodon", "failed with "+secret)), code: "upstream_error"},
		"unsupported_url":                {url: "http://localhost/article", code: "unsupported_url"},
		"private address":                {url: "http://10.0.0.5/x", code: "unsupported_url"},
		"page without text":              {mod: ret(&ss.Result{Platform: "web", URLKind: "page"}, nil), url: "https://example.com/a", code: job.CodeScraperUnavailable},
		"internal_error":                 {mod: ret(nil, errors.New("boom")), code: job.CodeScraperUnavailable},
		"panic":                          {mod: func(context.Context, ss.Target, ss.Request) (*ss.Result, error) { panic("boom " + secret) }, code: job.CodeScraperUnavailable},
		"nil result":                     {mod: ret(nil, nil), code: job.CodeScraperUnavailable},
		"missing platform":               {mod: ret(&ss.Result{URLKind: "profile"}, nil), code: job.CodeScraperUnavailable},
		"missing url_kind":               {mod: ret(&ss.Result{Platform: "mastodon"}, nil), code: job.CodeScraperUnavailable},
		"post kind without post":         {mod: ret(&ss.Result{Platform: "mastodon", URLKind: "post"}, nil), code: job.CodeScraperUnavailable},
		"post without text":              {mod: ret(&ss.Result{Platform: "mastodon", URLKind: "post", Post: &ss.Post{Text: "  "}}, nil), code: job.CodeNoTextContent},
		"profile without text":           {mod: ret(&ss.Result{Platform: "mastodon", URLKind: "profile", Posts: []ss.Post{{Text: ""}}}, nil), code: job.CodeNoTextContent},
	} {
		t.Run(name, func(t *testing.T) {
			url := c.url
			if url == "" {
				url = "https://mastodon.social/@alice"
			}
			se := scrapeErr(t, client(c.mod), context.Background(), url)
			require.Equal(t, c.code, se.Code)
			require.NotEmpty(t, se.Message)
			if c.details != "" {
				require.JSONEq(t, c.details, string(se.Details))
			}
		})
	}
}

func blocking(ctx context.Context, _ ss.Target, _ ss.Request) (*ss.Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestTimeoutAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.Equal(t, job.CodeTimeout, scrapeErr(t, client(moduleFunc(blocking)), ctx, "https://mastodon.social/@alice").Code)

	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	require.Equal(t, job.CodeCancelled, scrapeErr(t, client(moduleFunc(blocking)), ctx, "https://mastodon.social/@alice").Code)
}

func TestResultMapping(t *testing.T) {
	created := "2026-11-13T09:00:00+01:00"
	c := client(moduleFunc(func(_ context.Context, tg ss.Target, _ ss.Request) (*ss.Result, error) {
		return &ss.Result{Platform: tg.Platform, URLKind: tg.URLKind, Username: "alice@mastodon.social", DisplayName: ss.Ptr("Alice"),
			Post: &ss.Post{ID: "1", Text: "Vote no!", CreatedAt: &created}, Warnings: []ss.Warning{{Code: "comments_unavailable", Message: "x"}}}, nil
	}))
	url := "https://mastodon.social/@alice/1"
	res, err := c.Scrape(context.Background(), req(url))
	require.NoError(t, err)
	require.Equal(t, "mastodon", res.Platform)
	require.Equal(t, "post", res.URLKind)
	require.Len(t, res.Warnings, 1)
	in := res.Input
	require.Equal(t, "Vote no!", in.Content)
	require.Equal(t, "text/plain", in.ContentType)
	require.Equal(t, job.Source{URL: url, Title: "Alice", Author: "alice@mastodon.social", Platform: "mastodon",
		PublishedAt: job.Ptr(time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)), Metadata: in.Source.Metadata}, in.Source)
	var meta struct{ Scrape map[string]any }
	require.NoError(t, json.Unmarshal(in.Source.Metadata, &meta))
	require.Equal(t, "post", meta.Scrape["url_kind"])
	require.Equal(t, []any{}, meta.Scrape["comments"], "the core's ScrapeResult is embedded with [] for empty lists")
	require.Contains(t, meta.Scrape, "scraped_at")
}
