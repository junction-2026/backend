package socialscraper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const token = "SENTINEL-tok-9f8e7d6c5b4a"

type moduleFunc func(ctx context.Context, t Target, req Request) (*Result, error)

func (f moduleFunc) Scrape(ctx context.Context, t Target, req Request) (*Result, error) {
	return f(ctx, t, req)
}

// newTestScraper registers mod for every platform and logs through the redactor.
func newTestScraper(mod Module) (*Scraper, *bytes.Buffer) {
	reg := NewRegistry()
	for _, p := range []string{Bluesky, Mastodon} {
		reg.Register(p, mod)
	}
	var logs bytes.Buffer
	return New(reg, NewLogger(&logs, NewRedactor(env(map[string]string{"SCRAPER_TOKEN": token})))), &logs
}

func scrape(s *Scraper, url string) (*Result, *Error) {
	return s.Scrape(context.Background(), url, Request{MaxPosts: 20, MaxComments: 50})
}

func TestScrapeFillsTimesAndEmptySlices(t *testing.T) {
	var got Request
	var target Target
	s, _ := newTestScraper(moduleFunc(func(_ context.Context, t Target, req Request) (*Result, error) {
		got, target = req, t
		return &Result{Platform: t.Platform, URLKind: t.URLKind, Username: "alice"}, nil
	}))
	res, err := scrape(s, "https://bsky.app/profile/alice.bsky.social?x=1")
	require.Nil(t, err)
	assert.Equal(t, Request{MaxPosts: 20, MaxComments: 50}, got)
	assert.Equal(t, map[string]string{"actor": "alice.bsky.social"}, target.IDs)

	b, _ := json.Marshal(res)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Equal(t, "bluesky", m["platform"])
	assert.Regexp(t, `^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`, m["scraped_at"])
	assert.Equal(t, []any{}, m["posts"])
	assert.Equal(t, []any{}, m["comments"])
	assert.Equal(t, []any{}, m["warnings"])
	assert.Contains(t, m, "post")
	assert.Nil(t, m["followers"])
	assert.IsType(t, float64(0), m["latency_ms"])
}

func TestScrapeUnsupportedWithoutModuleCall(t *testing.T) {
	s, _ := newTestScraper(moduleFunc(func(context.Context, Target, Request) (*Result, error) {
		t.Error("the module must not be called")
		return nil, nil
	}))
	for _, u := range []string{"ftp://example.com/a", "http://127.0.0.1/admin", "http://localhost/", "http://10.0.0.5/x",
		"http://[::1]/", "https://user:secret@news.example.org/a", "https://exa mple.com/a"} {
		_, err := scrape(s, u)
		require.NotNil(t, err, u)
		assert.Equal(t, "unsupported_url", err.Code, u)
	}
}

func TestWebRoutedToWebModule(t *testing.T) {
	reg := NewRegistry()
	var got []Target
	reg.Register(Web, moduleFunc(func(_ context.Context, t Target, _ Request) (*Result, error) {
		got = append(got, t)
		return &Result{Platform: Web, URLKind: KindPage, Page: &Page{Text: "x"}}, nil
	}))
	reg.Register(Bluesky, moduleFunc(func(context.Context, Target, Request) (*Result, error) {
		t.Error("bluesky module called")
		return nil, nil
	}))
	s := New(reg, nil)
	for _, u := range []string{"https://www.dw.com/en/a/a-1", "https://bsky.app/search?q=x", "https://x.com/alice/status/1"} {
		res, err := scrape(s, u)
		require.Nil(t, err, u)
		assert.Equal(t, Web, res.Platform)
	}
	require.Len(t, got, 3)
	assert.Equal(t, "https://www.dw.com/en/a/a-1", got[0].URL.String())

	// no web module registered: platform_not_configured, never unsupported_url
	_, err := scrape(New(NewRegistry(), nil), "https://example.com/a")
	require.NotNil(t, err)
	assert.Equal(t, "platform_not_configured", err.Code)
	assert.Equal(t, "web", err.Details["platform"])
}

func TestMastodonProbeFallsBackToWeb(t *testing.T) {
	reg := NewRegistry()
	var order []string
	reg.Register(Mastodon, moduleFunc(func(context.Context, Target, Request) (*Result, error) {
		order = append(order, "mastodon")
		return nil, UnsupportedURL(Mastodon, "the host does not serve the Mastodon REST API")
	}))
	reg.Register(Web, moduleFunc(func(_ context.Context, tg Target, _ Request) (*Result, error) {
		order = append(order, "web")
		assert.Equal(t, KindPage, tg.URLKind)
		return &Result{Platform: Web, URLKind: KindPage, Page: &Page{Text: "x"}}, nil
	}))
	res, err := scrape(New(reg, nil), "https://blog.example.com/@alice")
	require.Nil(t, err)
	assert.Equal(t, Web, res.Platform)
	assert.Equal(t, []string{"mastodon", "web"}, order)

	// an error of a host that does serve the API is kept
	reg.Register(Mastodon, moduleFunc(func(context.Context, Target, Request) (*Result, error) {
		return nil, AccountNotFound(Mastodon, "gone")
	}))
	_, err = scrape(New(reg, nil), "https://mastodon.social/@nobody")
	require.NotNil(t, err)
	assert.Equal(t, "account_not_found", err.Code)
}

func TestPageResultJSON(t *testing.T) {
	s := New(NewRegistry(), nil)
	s.reg.Register(Web, moduleFunc(func(context.Context, Target, Request) (*Result, error) {
		return &Result{Platform: Web, URLKind: KindPage, Username: "must be dropped", Page: &Page{Text: "hello", Host: "example.com"}}, nil
	}))
	res, err := scrape(s, "https://example.com/a")
	require.Nil(t, err)
	b, _ := json.Marshal(res)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Contains(t, m, "username")
	assert.Nil(t, m["username"])
	assert.Equal(t, "hello", m["text"])
	assert.Equal(t, "example.com", m["host"])
	assert.Equal(t, []any{}, m["posts"])
	assert.Nil(t, m["post"])
	assert.Equal(t, []any{}, m["comments"])
}

func TestPlatformNotConfigured(t *testing.T) {
	s := New(NewRegistry(), nil)
	for url, platform := range map[string]string{
		"https://bsky.app/profile/alice.bsky.social": "bluesky",
		"https://mastodon.social/@alice":             "mastodon",
	} {
		_, err := scrape(s, url)
		require.NotNil(t, err, url)
		assert.Equal(t, "platform_not_configured", err.Code, url)
		assert.Equal(t, platform, err.Details["platform"], url)
	}

	reg := NewRegistry() // the Mastodon module is missing, Bluesky is registered
	reg.Register(Bluesky, moduleFunc(func(context.Context, Target, Request) (*Result, error) { return nil, nil }))
	_, err := scrape(New(reg, nil), "https://mastodon.social/@alice")
	require.NotNil(t, err)
	assert.Equal(t, "platform_not_configured", err.Code)
	assert.Equal(t, map[string]any{"platform": "mastodon"}, err.Details)
}

func TestModuleTypedErrorGetsPlatformAndSharedErrorIsNotMutated(t *testing.T) {
	shared := &Error{Code: "upstream_error", Message: "x"} // nil Details, shared by every call
	s, _ := newTestScraper(moduleFunc(func(_ context.Context, t Target, _ Request) (*Result, error) {
		if t.Platform == Mastodon {
			return nil, RateLimited("", 42)
		}
		return nil, shared
	}))
	_, err := scrape(s, "https://mastodon.social/@alice")
	assert.Equal(t, map[string]any{"platform": "mastodon", "retry_after_s": 42}, err.Details)
	for range 3 {
		_, err = scrape(s, "https://bsky.app/profile/alice.bsky.social")
		assert.Equal(t, "upstream_error", err.Code)
		assert.Equal(t, "bluesky", err.Details["platform"])
	}
	assert.Nil(t, shared.Details)
}

func TestPanicAndPlainErrorAreInternalWithoutLeaks(t *testing.T) {
	s, logs := newTestScraper(moduleFunc(func(_ context.Context, t Target, _ Request) (*Result, error) {
		if t.IDs["actor"] == "panics" {
			panic("secret-panic-detail " + token)
		}
		return nil, errors.New("Authorization: Bearer abc " + token)
	}))
	for _, actor := range []string{"panics", "errors", "panics"} { // the scraper survives a panic
		_, err := scrape(s, "https://bsky.app/profile/"+actor)
		require.NotNil(t, err)
		assert.Equal(t, "internal_error", err.Code)
		for _, bad := range []string{"goroutine", "panic", ".go:", "secret-panic-detail", "Bearer", token} {
			assert.NotContains(t, err.Message, bad)
		}
	}
	assert.Contains(t, logs.String(), "module panicked")
	assert.Contains(t, logs.String(), "stack") // the trace is in the log, redacted
	assert.NotContains(t, logs.String(), token)
	assert.NotContains(t, logs.String(), "Bearer abc")
}

func TestContextBoundsTheScrape(t *testing.T) {
	for name, fn := range map[string]moduleFunc{
		"honours ctx": func(ctx context.Context, _ Target, _ Request) (*Result, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		"ignores ctx": func(context.Context, Target, Request) (*Result, error) {
			time.Sleep(300 * time.Millisecond)
			return &Result{}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := newTestScraper(fn)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := s.Scrape(ctx, "https://bsky.app/profile/alice.bsky.social", Request{MaxPosts: 20})
			require.NotNil(t, err)
			assert.Equal(t, "upstream_timeout", err.Code)
			assert.Equal(t, "bluesky", err.Details["platform"])
			assert.Less(t, time.Since(start), 250*time.Millisecond)
		})
	}
}
