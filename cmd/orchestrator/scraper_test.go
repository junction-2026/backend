package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/registry"
	"github.com/junction-2026/backend/internal/socialscraper/scrapertest"
)

const sentinelToken = "SENTINEL-tok-6f1c9d2b"

func TestScraperConfig(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	c, err := loadConfig(get(nil))
	require.NoError(t, err)
	require.True(t, c.ScraperEnabled, "scraping is on by default")
	require.Equal(t, 30*time.Second, c.Upstream.Timeout)
	require.Empty(t, c.Ignored)

	for _, v := range []string{"false", "FALSE", "False"} {
		c, err = loadConfig(get(map[string]string{"SCRAPER_ENABLED": v}))
		require.NoError(t, err)
		require.False(t, c.ScraperEnabled, v)
	}
	c, err = loadConfig(get(map[string]string{"SCRAPER_ENABLED": "TRUE", "SCRAPER_TIMEOUT": "45s", "SCRAPER_UPSTREAM_TIMEOUT_SECONDS": "2.5"}))
	require.NoError(t, err)
	require.True(t, c.ScraperEnabled)
	require.Equal(t, 45*time.Second, c.Engine.ScrapeTimeout)
	require.Equal(t, 2500*time.Millisecond, c.Upstream.Timeout)

	c, err = loadConfig(get(map[string]string{"SCRAPER_URL": "http://secret-host:8003", "SCRAPER_TOKEN": "s3cret", "SCRAPER_LISTEN_ADDR": ":8003"}))
	require.NoError(t, err, "obsolete variables do not abort startup")
	require.Equal(t, []string{"SCRAPER_URL", "SCRAPER_TOKEN", "SCRAPER_LISTEN_ADDR"}, c.Ignored)

	for name, vars := range map[string]map[string]string{
		"SCRAPER_ENABLED":                  {"SCRAPER_ENABLED": "s3cret-yes"},
		"SCRAPER_TIMEOUT":                  {"SCRAPER_TIMEOUT": "-1s"},
		"SCRAPER_UPSTREAM_TIMEOUT_SECONDS": {"SCRAPER_UPSTREAM_TIMEOUT_SECONDS": "s3cret"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(get(vars))
			require.ErrorContains(t, err, name)
			require.NotContains(t, err.Error(), "s3cret")
		})
	}
}

func TestInvalidScraperEnabledAbortsStartup(t *testing.T) {
	err := run(t.Context(), env(map[string]string{"SCRAPER_ENABLED": "yes", "LISTEN_ADDR": "127.0.0.1:0"}), nil, nil)
	require.ErrorContains(t, err, "SCRAPER_ENABLED")
}

func TestBotDetectionExampleLoadsDisabled(t *testing.T) {
	reg, err := registry.Load("../../docs/examples/bot_detection.yaml", registry.Options{
		Getenv: func(string) (string, bool) { return "", false }, HandlerExists: func(string) bool { return false }})
	require.NoError(t, err)
	a := reg.Get("bot_detection")
	require.NotNil(t, a)
	require.False(t, a.Enabled)
	require.Equal(t, "http_sync", a.Execution.Adapter)
	require.Equal(t, map[string]string{"content": "$input", "source": "$input.source"}, a.Input.Bindings)
}

func fixture(t *testing.T, platform, name string) []byte {
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "socialscraper", platform, "testdata", name))
	require.NoError(t, err)
	return b
}

// platforms routes every platform host to its recorded fixtures (see the module tests).
func platforms(t *testing.T) *scrapertest.Router {
	serve := func(platform string, pick func(r *http.Request) string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			name := pick(r)
			if name == "" {
				t.Errorf("unexpected %s request %s", platform, r.URL)
				w.WriteHeader(500)
				return
			}
			ct := "application/json"
			if strings.HasSuffix(name, ".html") {
				ct = "text/html"
			}
			scrapertest.Body(200, fixture(t, platform, name), "Content-Type", ct)(w, r)
		}
	}
	bluesky := serve("bluesky", func(r *http.Request) string {
		switch {
		case strings.HasSuffix(r.URL.Path, "/app.bsky.actor.getProfile"):
			return "profile.json"
		case strings.HasSuffix(r.URL.Path, "/app.bsky.feed.getAuthorFeed") && r.URL.Query().Get("cursor") == "page2":
			return "author_feed_page2.json"
		case strings.HasSuffix(r.URL.Path, "/app.bsky.feed.getAuthorFeed"):
			return "author_feed_page1.json"
		}
		return ""
	})
	mastodon := serve("mastodon", func(r *http.Request) string {
		return map[string]string{"/api/v1/accounts/lookup": "account_alice.json", "/api/v1/accounts/1001/statuses": "statuses_page1.json"}[r.URL.Path]
	})
	return scrapertest.New(t).Handle("public.api.bsky.app", bluesky).Handle("mastodon.example", mastodon)
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// useTransport routes the orchestrator's platform requests through rt for this test.
func useTransport(t *testing.T, rt http.RoundTripper) {
	upstreamTransport = rt
	t.Cleanup(func() { upstreamTransport = nil })
}

func (s *server) waitDone(t *testing.T, id string) map[string]any {
	t.Helper()
	var j map[string]any
	require.Eventually(t, func() bool {
		_, j = s.json(t, "GET", "/api/v1/jobs/"+id, "")
		return j["done"] == true
	}, 10*time.Second, 10*time.Millisecond)
	return j
}

func (s *server) scrapeJob(t *testing.T, url string) map[string]any {
	t.Helper()
	code, created := s.json(t, "POST", "/api/v1/jobs", `{"scrape":{"url":"`+url+`"}}`)
	require.Equal(t, 201, code, created)
	require.Nil(t, created["input"])
	return s.waitDone(t, created["id"].(string))
}

// Jobs created from a URL run end to end inside the single orchestrator: no SCRAPER_URL, no
// scraper service, platform requests served offline from the modules' fixtures.
func TestEndToEndScrapeJobs(t *testing.T) {
	router := platforms(t)
	useTransport(t, router)
	logs := &syncBuf{}
	s := startLog(t, map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers",
		"SCRAPER_TOKEN": sentinelToken}, logs)

	for url, platform := range map[string]string{
		"https://bsky.app/profile/alice.bsky.social": "bluesky",
		"https://mastodon.example/@alice":            "mastodon",
	} {
		t.Run(platform, func(t *testing.T) {
			j := s.scrapeJob(t, url)
			sc := j["scrape"].(map[string]any)
			require.Equal(t, "succeeded", sc["status"], sc["error"])
			require.Equal(t, "completed", j["status"])
			require.Equal(t, platform, sc["platform"])
			in := j["input"].(map[string]any)
			require.NotEmpty(t, in["content"])
			src := in["source"].(map[string]any)
			require.Equal(t, platform, src["platform"])
			require.Equal(t, platform, src["metadata"].(map[string]any)["scrape"].(map[string]any)["platform"])
		})
	}

	before := len(router.Requests())
	for _, url := range []string{"https://x.com/alice", "https://x.com/alice/status/1790000000000000000",
		"https://www.facebook.com/examplenews", "https://www.facebook.com/profile.php?id=100000000000001"} {
		j := s.scrapeJob(t, url)
		require.Equal(t, "failed", j["status"])
		require.Equal(t, "unsupported_url", j["scrape"].(map[string]any)["error"].(map[string]any)["code"])
	}
	require.Len(t, router.Requests(), before, "unsupported social network URLs reach no host")

	require.NotContains(t, logs.String(), sentinelToken)
}

// A panic in a platform module fails that job with scraper_unavailable; the server keeps serving.
func TestModulePanicKeepsServing(t *testing.T) {
	router := platforms(t)
	useTransport(t, rtFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "mastodon.example" {
			panic("transport exploded with " + sentinelToken + " and Bearer abc.def")
		}
		return router.RoundTrip(r)
	}))
	logs := &syncBuf{}
	s := startLog(t, map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers",
		"SCRAPER_TOKEN": sentinelToken}, logs)

	j := s.scrapeJob(t, "https://mastodon.example/@alice")
	require.Equal(t, "failed", j["status"])
	e := j["scrape"].(map[string]any)["error"].(map[string]any)
	require.Equal(t, "scraper_unavailable", e["code"])

	j = s.scrapeJob(t, "https://bsky.app/profile/alice.bsky.social")
	require.Equal(t, "completed", j["status"])

	require.Contains(t, logs.String(), "module panicked")
	require.Contains(t, logs.String(), "[redacted]")
	body, _ := json.Marshal(e)
	for _, secret := range []string{sentinelToken, "abc.def"} {
		require.NotContains(t, logs.String(), secret)
		require.NotContains(t, string(body), secret)
	}
}

func TestScrapeDisabled(t *testing.T) {
	s := start(t, map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers", "SCRAPER_ENABLED": "false"})
	code, body := s.json(t, "POST", "/api/v1/jobs", `{"scrape":{"url":"https://mastodon.example/@alice"}}`)
	require.Equal(t, 503, code)
	require.Equal(t, "scrape_unavailable", body["error"].(map[string]any)["code"])
	_, page := s.json(t, "GET", "/api/v1/jobs", "")
	require.Empty(t, page["items"])
}

// Obsolete variables of the former scraper service are named in one warning each, never their values.
func TestObsoleteScraperVariablesWarned(t *testing.T) {
	useTransport(t, platforms(t))
	logs := &syncBuf{}
	startLog(t, map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers",
		"SCRAPER_URL": "http://old-scraper-host:8003", "SCRAPER_TOKEN": "old-s3cret-token", "SCRAPER_LISTEN_ADDR": "0.0.0.0:8099"}, logs)
	for _, name := range obsoleteScraperVars {
		require.Equal(t, 1, strings.Count(logs.String(), `"variable":"`+name+`"`), name)
	}
	for _, value := range []string{"old-scraper-host", "old-s3cret-token", "8099"} {
		require.NotContains(t, logs.String(), value)
	}
}
