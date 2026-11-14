package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/registry"
)

func TestScraperConfig(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	c, err := loadConfig(get(nil))
	require.NoError(t, err)
	require.Nil(t, c.Engine.Scraper, "unset SCRAPER_URL: no scraper")

	c, err = loadConfig(get(map[string]string{"SCRAPER_URL": "http://scraper:8003", "SCRAPER_TOKEN": "tok", "SCRAPER_TIMEOUT": "45s"}))
	require.NoError(t, err)
	require.Equal(t, "http://scraper:8003", c.Engine.Scraper.URL)
	require.Equal(t, "tok", c.Engine.Scraper.Token)
	require.Equal(t, 45*time.Second, c.Engine.ScrapeTimeout)

	for name, vars := range map[string]map[string]string{
		"SCRAPER_TOKEN":   {"SCRAPER_URL": "http://secret-host:8003"},
		"SCRAPER_URL":     {"SCRAPER_URL": "secret-host:8003", "SCRAPER_TOKEN": "s3cret"},
		"SCRAPER_TIMEOUT": {"SCRAPER_URL": "http://secret-host:8003", "SCRAPER_TOKEN": "s3cret", "SCRAPER_TIMEOUT": "-1s"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(get(vars))
			require.ErrorContains(t, err, name)
			require.NotContains(t, err.Error(), "secret-host")
			require.NotContains(t, err.Error(), "s3cret")
		})
	}
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

// A job created from a URL runs end to end through the real scraper client.
func TestEndToEndScrapeJob(t *testing.T) {
	const token = "scraper-s3cret-token"
	scraper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, `{"platform":"bluesky","url_kind":"post","username":"alice.bsky.social","display_name":null,
"post":{"id":"3kxyz","text":"one two\nthree","created_at":null},"posts":[],"comments":[],"warnings":[]}`)
	}))
	defer scraper.Close()

	// no scraper configured: 503 and no job
	s := start(t, map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers"})
	code, body := s.json(t, "POST", "/api/v1/jobs", `{"scrape":{"url":"https://x.com/alice"}}`)
	require.Equal(t, 503, code)
	require.Equal(t, "scrape_unavailable", body["error"].(map[string]any)["code"])
	_, page := s.json(t, "GET", "/api/v1/jobs", "")
	require.Empty(t, page["items"])

	logs := &syncBuf{}
	s = startLog(t, map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers", "SCRAPER_URL": scraper.URL, "SCRAPER_TOKEN": token}, logs)
	code, created := s.json(t, "POST", "/api/v1/jobs", `{"scrape":{"url":"https://bsky.app/profile/alice.bsky.social/post/3kxyz"}}`)
	require.Equal(t, 201, code)
	require.Nil(t, created["input"])
	id := created["id"].(string)
	var j map[string]any
	require.Eventually(t, func() bool {
		_, j = s.json(t, "GET", "/api/v1/jobs/"+id, "")
		return j["done"] == true
	}, 10*time.Second, 10*time.Millisecond)
	require.Equal(t, "completed", j["status"])
	require.Equal(t, "one two\nthree", j["input"].(map[string]any)["content"])
	require.Equal(t, "succeeded", j["scrape"].(map[string]any)["status"])
	run := j["analyzers"].([]any)[0].(map[string]any)
	require.Equal(t, map[string]any{"characters": 13.0, "words": 3.0, "lines": 2.0}, run["result"])

	all, _ := json.Marshal(j)
	for _, secret := range []string{token, scraper.URL} {
		require.NotContains(t, string(all), secret)
		require.NotContains(t, logs.String(), secret)
	}
}
