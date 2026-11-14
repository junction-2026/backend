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
	assert.Equal(t, Config{Timeout: 30 * time.Second}, c)

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
