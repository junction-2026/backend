package socialscraper

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadConfig(t *testing.T) {
	c, err := LoadConfig(env(nil))
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, c.Timeout)

	c, err = LoadConfig(env(map[string]string{"SCRAPER_UPSTREAM_TIMEOUT_SECONDS": "2.5"}))
	require.NoError(t, err)
	assert.Equal(t, 2500*time.Millisecond, c.Timeout)
}

func TestLoadConfigErrorsNameTheVariableNotTheValue(t *testing.T) {
	for _, bad := range []string{"abc-secret", "0", "-3", "NaN", "Inf", "1e12"} {
		_, err := LoadConfig(env(map[string]string{"SCRAPER_UPSTREAM_TIMEOUT_SECONDS": bad}))
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "SCRAPER_UPSTREAM_TIMEOUT_SECONDS")
		assert.NotContains(t, err.Error(), bad)
	}
}

func TestWebConfig(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	c, err := LoadConfig(get(nil))
	require.NoError(t, err)
	assert.Equal(t, WebConfig{API: "web_api", MinTextChars: 300, CacheTTL: time.Hour, Timeout: 150 * time.Second}, c.Web)

	c, err = LoadConfig(get(map[string]string{"SCRAPER_UPSTREAM_TIMEOUT_SECONDS": "200"}))
	require.NoError(t, err)
	assert.Equal(t, 200*time.Second, c.Web.Timeout, "max(upstream, 150)")

	c, err = LoadConfig(get(map[string]string{"OXYLABS_API": "web_scraper_api", "OXYLABS_USERNAME": "u", "OXYLABS_PASSWORD": "p",
		"OXYLABS_LOCATION": "de", "WEB_MIN_TEXT_CHARS": "100", "WEB_CACHE_TTL": "5m", "WEB_TIMEOUT_SECONDS": "90"}))
	require.NoError(t, err)
	assert.Equal(t, WebConfig{API: "web_scraper_api", Username: "u", Password: "p", Location: "DE", MinTextChars: 100,
		CacheTTL: 5 * time.Minute, Timeout: 90 * time.Second}, c.Web)

	for name, v := range map[string]string{"OXYLABS_API": "bogus", "OXYLABS_LOCATION": "DEU", "WEB_MIN_TEXT_CHARS": "0",
		"WEB_CACHE_TTL": "-1h", "WEB_TIMEOUT_SECONDS": "abc"} {
		_, err := LoadConfig(get(map[string]string{name: v + "s3cret"}))
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), name)
		assert.NotContains(t, err.Error(), "s3cret")
	}
}

func TestAIStudioConfig(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	c, err := LoadConfig(get(map[string]string{"OXYLABS_API": "ai_studio"}))
	require.NoError(t, err, "a missing key does not abort startup")
	assert.Equal(t, APIAIStudio, c.Web.API)
	assert.Empty(t, c.Web.AIStudioKey)
	assert.Empty(t, c.Web.AIStudioURL, "empty means DefaultAIStudioURL")

	c, err = LoadConfig(get(map[string]string{"OXYLABS_API": "ai_studio", "OXYLABS_AI_STUDIO_API_KEY": " k ", "OXYLABS_AI_STUDIO_URL": "http://localhost:9000/"}))
	require.NoError(t, err)
	assert.Equal(t, "k", c.Web.AIStudioKey)
	assert.Equal(t, "http://localhost:9000", c.Web.AIStudioURL)

	for _, bad := range []string{"api-aistudio.oxylabs.io", "ftp://x.example", "/relative", "https://"} {
		_, err := LoadConfig(get(map[string]string{"OXYLABS_AI_STUDIO_URL": bad}))
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "OXYLABS_AI_STUDIO_URL")
		assert.NotContains(t, err.Error(), bad)
	}
}

func TestRedactorCoversAIStudioKey(t *testing.T) {
	r := NewRedactor(env(map[string]string{"OXYLABS_AI_STUDIO_API_KEY": "aistudio-key-77"}))
	assert.NotContains(t, r.Redact("x-api-key aistudio-key-77"), "aistudio-key-77")
}
