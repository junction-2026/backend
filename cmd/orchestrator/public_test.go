package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublicConfigDefaultsAndValues(t *testing.T) {
	c, err := loadConfig(cfgOf(nil))
	require.NoError(t, err)
	require.Equal(t, 30, c.PublicSubmitPerMinute)
	require.Equal(t, 600, c.PublicGetPerMinute)
	require.Empty(t, c.PublicBaseURL)

	c, err = loadConfig(cfgOf(map[string]string{"PUBLIC_API_SUBMIT_PER_MINUTE": "5", "PUBLIC_API_GET_PER_MINUTE": "7", "PUBLIC_BASE_URL": "https://veil.example.org/"}))
	require.NoError(t, err)
	require.Equal(t, 5, c.PublicSubmitPerMinute)
	require.Equal(t, 7, c.PublicGetPerMinute)
	require.Equal(t, "https://veil.example.org/", c.PublicBaseURL)
}

func TestPublicConfigErrorsNameTheVariable(t *testing.T) {
	for _, c := range []struct{ name, value string }{
		{"PUBLIC_API_SUBMIT_PER_MINUTE", "0"}, {"PUBLIC_API_SUBMIT_PER_MINUTE", "1.5"}, {"PUBLIC_API_SUBMIT_PER_MINUTE", "many"},
		{"PUBLIC_API_GET_PER_MINUTE", "-1"}, {"PUBLIC_API_GET_PER_MINUTE", "x"},
		{"PUBLIC_BASE_URL", "veil.example.org"}, {"PUBLIC_BASE_URL", "ftp://veil.example.org"}, {"PUBLIC_BASE_URL", "/jobs"},
	} {
		_, err := loadConfig(cfgOf(map[string]string{c.name: c.value}))
		require.ErrorContains(t, err, c.name, c.value)
	}
}

// The whole process: the public API creates an ordinary job that finishes and is readable on both paths.
func TestPublicAnalysisEndToEnd(t *testing.T) {
	s := start(t, map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers", "PUBLIC_BASE_URL": "https://veil.example.org/"})
	code, created := s.json(t, "POST", "/api/v1/public/analyses", `{"post":"Vote no!","comments":["Agreed"]}`)
	require.Equal(t, 201, code, created)
	id := created["id"].(string)
	require.Equal(t, "https://veil.example.org/jobs/"+id, created["share_url"])
	var got map[string]any
	require.Eventually(t, func() bool {
		_, got = s.json(t, "GET", "/api/v1/public/analyses/"+id, "")
		return got["done"] == true
	}, 10*time.Second, 10*time.Millisecond)
	require.Equal(t, "completed", got["status"])
	require.Equal(t, "Post:\nVote no!\n\nComment 1:\nAgreed", got["input"].(map[string]any)["content"])

	code, j := s.json(t, "GET", "/api/v1/jobs/"+id, "")
	require.Equal(t, 200, code)
	require.Equal(t, true, j["options"].(map[string]any)["finalize"])
	_, reg := s.json(t, "GET", "/api/v1/analyzers", "")
	var want, got2 []string
	for _, a := range reg["items"].([]any) {
		if a.(map[string]any)["selectable"] == true {
			want = append(want, a.(map[string]any)["id"].(string))
		}
	}
	for _, a := range j["analyzers"].([]any) {
		got2 = append(got2, a.(map[string]any)["id"].(string))
	}
	require.NotEmpty(t, want)
	require.ElementsMatch(t, want, got2, "every non-finalizer registry analyzer")
	require.Equal(t, map[string]any{"public_api": map[string]any{"has_post": true, "comment_count": float64(1)}},
		j["input"].(map[string]any)["source"].(map[string]any)["metadata"])
}
