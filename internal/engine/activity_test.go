package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func def(id, adapterName string, mod func(*registry.Execution)) *registry.Analyzer {
	a := &registry.Analyzer{ID: id, Name: "Name of " + id, Execution: registry.Execution{Adapter: adapterName}}
	if mod != nil {
		mod(&a.Execution)
	}
	return a
}

func TestActivityNewsSearchLanguages(t *testing.T) {
	d := def("news_search", registry.AdapterNewsSearch, func(e *registry.Execution) {
		e.News.Languages = []string{"source", "en"}
	})
	tr := func(lang string) map[string]any {
		return map[string]any{"translation": raw(`{"source_language":"` + lang + `"}`)}
	}
	for name, c := range map[string]struct {
		in     map[string]any
		detail any
		langs  any
	}{
		"german":         {tr("de"), "in German and English", []string{"de", "en"}},
		"english dedup":  {tr("en"), "in English", []string{"en"}},
		"unlisted code":  {tr("ja"), "in ja and English", []string{"ja", "en"}},
		"unknown source": {map[string]any{"translation": raw(`{}`)}, "in English", []string{"en"}},
	} {
		t.Run(name, func(t *testing.T) {
			a := activityFor(d, c.in)
			require.Equal(t, "news_search", a.Key)
			require.Equal(t, "Searching news coverage", a.Text)
			if c.detail == nil {
				require.Nil(t, a.Detail)
				require.Empty(t, a.Params)
				return
			}
			require.Equal(t, c.detail, *a.Detail)
			require.Equal(t, map[string]any{"languages": c.langs}, a.Params)
		})
	}
	only := def("news_search", registry.AdapterNewsSearch, func(e *registry.Execution) { e.News.Languages = []string{"source"} })
	a := activityFor(only, map[string]any{"translation": raw(`{}`)})
	require.Nil(t, a.Detail, "nothing determinable")
	require.Empty(t, a.Params)
	three := def("news_search", registry.AdapterNewsSearch, func(e *registry.Execution) { e.News.Languages = []string{"de", "fr", "uk"} })
	require.Equal(t, "in German, French and Ukrainian", *activityFor(three, nil).Detail)
}

func TestActivityFactCheckClaims(t *testing.T) {
	d := def("fact_check", registry.AdapterFactCheck, nil) // min worth 0.4, max 3 claims
	claims := func(ws ...string) map[string]any {
		var cs []string
		for _, w := range ws {
			cs = append(cs, `{"text":"t","check_worthiness":`+w+`}`)
		}
		return map[string]any{"extraction": raw(`{"claims":[` + strings.Join(cs, ",") + `]}`)}
	}
	for name, c := range map[string]struct {
		in     map[string]any
		claims any
		detail string
	}{
		"two qualify":     {claims("0.9", "0.1", "0.4"), 2, "for 2 claims"},
		"one":             {claims("0.5"), 1, "for 1 claim"},
		"capped at three": {claims("0.9", "0.9", "0.9", "0.9"), 3, "for 3 claims"},
		"none":            {claims("0.1"), 0, "for 0 claims"},
		"unreadable":      {map[string]any{"extraction": "x"}, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			a := activityFor(d, c.in)
			require.Equal(t, "Looking up fact-checks", a.Text)
			if c.claims == nil {
				require.Nil(t, a.Detail)
				require.Empty(t, a.Params)
				return
			}
			require.Equal(t, c.detail, *a.Detail)
			require.Equal(t, map[string]any{"claims": c.claims}, a.Params)
		})
	}
}

func TestActivityTechniqueModels(t *testing.T) {
	d := def("techniques", registry.AdapterTechniqueClassifier, nil) // technique_min_words 40, social_max_words 80
	text := func(n int) map[string]any {
		return map[string]any{"text": raw(`{"translated_text":"` + strings.TrimSpace(strings.Repeat("w ", n)) + `","source_language":"en"}`)}
	}
	for name, c := range map[string]struct {
		words  int
		models []string
		detail string
	}{
		"short":    {30, []string{"nci-social"}, "short-post model"},
		"both":     {40, []string{"nci-technique", "nci-social"}, "document model and short-post model"},
		"long":     {81, []string{"nci-technique"}, "document model"},
		"upper 80": {80, []string{"nci-technique", "nci-social"}, "document model and short-post model"},
	} {
		t.Run(name, func(t *testing.T) {
			a := activityFor(d, text(c.words))
			require.Equal(t, "Detecting persuasion techniques", a.Text)
			require.Equal(t, c.detail, *a.Detail)
			require.Equal(t, map[string]any{"models": c.models}, a.Params)
		})
	}
	a := activityFor(d, map[string]any{"text": raw(`{}`)})
	require.Nil(t, a.Detail)
	require.Empty(t, a.Params)
}

func TestActivityLLMTitleChecked(t *testing.T) {
	d := def("llm_techniques", registry.AdapterLLMTechniques, nil)
	for name, c := range map[string]struct {
		content, title string
		checked        bool
	}{
		"headline": {"body text", "A headline", true},
		"no title": {"body text", "", false},
		"same":     {"body text", " body text ", false},
	} {
		t.Run(name, func(t *testing.T) {
			a := activityFor(d, map[string]any{"job_content": c.content, "job_title": c.title})
			require.Equal(t, map[string]any{"title_checked": c.checked}, a.Params)
			if c.checked {
				require.Equal(t, "including the headline", *a.Detail)
			} else {
				require.Nil(t, a.Detail)
			}
		})
	}
}

func TestActivityTextsAndKeys(t *testing.T) {
	for key, text := range map[string]string{"translation": "Translating the text", "propaganda_detection": "Checking for propaganda",
		"topic_extraction": "Finding topics and claims", "source_info": "Checking the news outlets"} {
		a := activityFor(def(key, "custom", nil), nil)
		require.Equal(t, job.Activity{Key: key, Text: text, Params: map[string]any{}}, a)
	}
	// unknown key: the analyzer name
	a := activityFor(def("bot_detection", "custom", nil), nil)
	require.Equal(t, job.Activity{Key: "bot_detection", Text: "Name of bot_detection", Params: map[string]any{}}, a)
	// a deployment key
	d := def("x", "custom", nil)
	d.UI.ActivityKey = "translation"
	require.Equal(t, "translation", activityFor(d, nil).Key)
	// a borrowed key with another adapter reads no facts
	d = def("x", "custom", nil)
	d.UI.ActivityKey = "news_search"
	require.Empty(t, activityFor(d, nil).Params)
}

func TestScrapeActivity(t *testing.T) {
	for url, host := range map[string]string{"https://www.bsky.app/profile/a.bsky.social": "bsky.app", "https://Mastodon.Social:443/@a": "mastodon.social"} {
		a := scrapeActivity(url)
		require.Equal(t, map[string]any{"host": host}, a.Params)
		require.Equal(t, "from "+host, *a.Detail)
	}
	a := scrapeActivity("not a url")
	require.Nil(t, a.Detail)
	require.Empty(t, a.Params)
}
