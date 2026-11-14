package socialscraper

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadConfig(t *testing.T) {
	c, err := LoadConfig(env(map[string]string{"SCRAPER_TOKEN": "tok"}))
	require.NoError(t, err)
	assert.Equal(t, Config{Token: "tok", Timeout: 30 * time.Second, ListenAddr: "0.0.0.0:8003"}, c)

	c, err = LoadConfig(env(map[string]string{"SCRAPER_TOKEN": "tok", "SCRAPER_UPSTREAM_TIMEOUT_SECONDS": "2.5", "SCRAPER_LISTEN_ADDR": ":9"}))
	require.NoError(t, err)
	assert.Equal(t, 2500*time.Millisecond, c.Timeout)
	assert.Equal(t, ":9", c.ListenAddr)
}

func TestLoadConfigErrorsNameTheVariableNotTheValue(t *testing.T) {
	_, err := LoadConfig(env(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SCRAPER_TOKEN")

	for _, bad := range []string{"abc-secret", "0", "-3", "NaN", "Inf", "1e12"} {
		_, err := LoadConfig(env(map[string]string{"SCRAPER_TOKEN": "tok-secret", "SCRAPER_UPSTREAM_TIMEOUT_SECONDS": bad}))
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "SCRAPER_UPSTREAM_TIMEOUT_SECONDS")
		assert.NotContains(t, err.Error(), bad)
		assert.NotContains(t, err.Error(), "tok-secret")
	}
}
