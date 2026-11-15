package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
)

func TestCountryConfig(t *testing.T) {
	c, err := loadConfig(cfgOf(map[string]string{"COUNTRY_ASSOCIATION_BASE_URL": "ftp://ignored"}))
	require.NoError(t, err, "without a model the step is not configured and nothing else is checked")
	require.Equal(t, adapter.CountryConfig{}, c.Country)
	require.Equal(t, 0, c.Engine.CountryBackfillBatch, "engine default (20) applies")

	c, err = loadConfig(cfgOf(map[string]string{"COUNTRY_ASSOCIATION_MODEL": "m", "COUNTRY_ASSOCIATION_BASE_URL": "https://llm.example/v1",
		"COUNTRY_ASSOCIATION_API_KEY": " k ", "COUNTRY_BACKFILL_BATCH": "5"}))
	require.NoError(t, err)
	require.Equal(t, adapter.CountryConfig{BaseURL: "https://llm.example/v1", APIKey: "k", Model: "m", Timeout: 30 * time.Second, MaxInputChars: 8000}, c.Country)
	require.Equal(t, 5, c.Engine.CountryBackfillBatch)

	c, err = loadConfig(cfgOf(map[string]string{"COUNTRY_ASSOCIATION_MODEL": "m", "COUNTRY_ASSOCIATION_BASE_URL": "http://llm",
		"COUNTRY_ASSOCIATION_TIMEOUT": "5s", "COUNTRY_ASSOCIATION_MAX_INPUT_CHARS": "100"}))
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, c.Country.Timeout)
	require.Equal(t, 100, c.Country.MaxInputChars)

	for name, vars := range map[string]map[string]string{
		"COUNTRY_ASSOCIATION_BASE_URL":        {"COUNTRY_ASSOCIATION_MODEL": "m"},
		"COUNTRY_ASSOCIATION_TIMEOUT":         {"COUNTRY_ASSOCIATION_MODEL": "m", "COUNTRY_ASSOCIATION_BASE_URL": "http://x", "COUNTRY_ASSOCIATION_TIMEOUT": "s3cr3tvalue"},
		"COUNTRY_ASSOCIATION_MAX_INPUT_CHARS": {"COUNTRY_ASSOCIATION_MODEL": "m", "COUNTRY_ASSOCIATION_BASE_URL": "http://x", "COUNTRY_ASSOCIATION_MAX_INPUT_CHARS": "0"},
		"COUNTRY_BACKFILL_BATCH":              {"COUNTRY_BACKFILL_BATCH": "0"},
	} {
		_, err := loadConfig(cfgOf(vars))
		require.ErrorContains(t, err, name)
		require.NotContains(t, err.Error(), "s3cr3tvalue", name)
	}
}
