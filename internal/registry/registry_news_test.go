package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newsYAML is a registry with a stand-in topic_extraction and a news_search whose execution is `exec`.
func newsYAML(exec, rest string) string {
	return "analyzers:\n  - id: topic_extraction\n" + custom + "  - id: translation\n" + custom +
		"  - id: news_search\n    depends_on: [topic_extraction, translation]\n" +
		"    input: {bindings: {topics: $analyzers.topic_extraction, translation: $analyzers.translation}}\n" +
		"    execution:\n      adapter: news_search\n" + exec + rest
}

const newsOK = "      base_url: http://serper/\n      api_key: ${SK}\n      translate: {api_key: \"${MK}\"}\n"

var newsEnv = map[string]string{"SK": "serper-key", "MK": "mistral-key"}

func TestNewsSearchDefaults(t *testing.T) {
	r, err := Load(write(t, "r.yaml", newsYAML(newsOK, "")), opts(newsEnv))
	require.NoError(t, err)
	e := r.Get("news_search").Execution
	require.Equal(t, "http://serper", e.BaseURL)
	require.Equal(t, "serper-key", e.APIKey)
	require.Equal(t, 20*time.Second, e.Timeout)
	require.Equal(t, NewsSearch{
		Languages: []string{"source", "en"},
		Translate: NewsTranslate{APIKey: "mistral-key", Model: "mistral-small-latest", Endpoint: DefaultMistralEndpoint},
		MaxTopics: 3, MaxNews: 2, Num: 8, MinResults: 2, PerDomain: 2, MaxResults: 12, MaxConcurrency: 4,
		CacheTTL: 6 * time.Hour,
	}, e.News)
}

func TestNewsSearchOverrides(t *testing.T) {
	r, err := Load(write(t, "r.yaml", newsYAML(newsOK,
		"      languages: [Source, DE, fr]\n      max_topics: 1\n      num: 3\n      cache_ttl: 90s\n      timeout: 5s\n")), opts(newsEnv))
	require.NoError(t, err)
	n := r.Get("news_search").Execution.News
	require.Equal(t, []string{"source", "de", "fr"}, n.Languages)
	require.Equal(t, 1, n.MaxTopics)
	require.Equal(t, 3, n.Num)
	require.Equal(t, 90*time.Second, n.CacheTTL)
	require.Equal(t, 5*time.Second, r.Get("news_search").Execution.Timeout)
}

func TestNewsSearchSourceOnlyNeedsNoTranslateKey(t *testing.T) {
	_, err := Load(write(t, "r.yaml", newsYAML("      base_url: http://s\n      api_key: k\n      languages: [source]\n", "")), opts(nil))
	require.NoError(t, err)
}

func TestNewsSearchValidation(t *testing.T) {
	for name, c := range map[string]struct{ exec, want string }{
		"no api_key":        {"      base_url: http://s\n      translate: {api_key: m}\n", "execution.api_key"},
		"no base_url":       {"      api_key: k\n      translate: {api_key: m}\n", "execution.base_url"},
		"no translate key":  {"      base_url: http://s\n      api_key: k\n", "execution.translate.api_key"},
		"empty translate":   {"      base_url: http://s\n      api_key: k\n      translate: {api_key: \"${E}\"}\n", "execution.translate.api_key"},
		"unset var":         {"      base_url: http://s\n      api_key: ${NOPE}\n      translate: {api_key: m}\n", "NOPE"},
		"zero num":          {newsOK + "      num: 0\n", "execution.num"},
		"negative topics":   {newsOK + "      max_topics: -1\n", "execution.max_topics"},
		"zero news":         {newsOK + "      max_news: 0\n", "execution.max_news"},
		"zero min_results":  {newsOK + "      min_results: 0\n", "execution.min_results"},
		"zero per_domain":   {newsOK + "      per_domain: 0\n", "execution.per_domain"},
		"zero max_results":  {newsOK + "      max_results: 0\n", "execution.max_results"},
		"zero concurrency":  {newsOK + "      max_concurrency: 0\n", "execution.max_concurrency"},
		"zero ttl":          {newsOK + "      cache_ttl: 0s\n", "execution.cache_ttl"},
		"unknown language":  {newsOK + "      languages: [german]\n", "execution.languages"},
		"empty languages":   {newsOK + "      languages: []\n", "execution.languages"},
		"endpoint not used": {newsOK + "      endpoint: http://x\n", "execution.endpoint"},
		"negative timeout":  {newsOK + "      timeout: -1s\n", "timeout"},
	} {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{"SK": "s", "MK": "m", "E": ""}
			_, err := Load(write(t, "r.yaml", newsYAML(c.exec, "")), opts(env))
			require.ErrorContains(t, err, c.want)
		})
	}
}

func TestNewsSearchBindingsAndDependencies(t *testing.T) {
	head := "analyzers:\n  - id: topic_extraction\n" + custom + "  - id: translation\n" + custom + "  - id: news_search\n"
	exec := "    execution:\n      adapter: news_search\n" + newsOK
	for name, c := range map[string]struct{ body, want string }{
		"no topic_extraction dep": {"    depends_on: [translation]\n    input: {bindings: {topics: $analyzers.translation}}\n" + exec, "depends_on must include topic_extraction"},
		"no bindings":             {"    depends_on: [topic_extraction]\n" + exec, "input.bindings.topics"},
		"wrong topics source":     {"    depends_on: [topic_extraction, translation]\n    input: {bindings: {topics: $analyzers.translation}}\n" + exec, "input.bindings.topics"},
		"translation not in deps": {"    depends_on: [topic_extraction]\n    input: {bindings: {topics: $analyzers.topic_extraction, translation: $analyzers.translation}}\n" + exec, "not in depends_on"},
		"unknown binding":         {"    depends_on: [topic_extraction]\n    input: {bindings: {topics: $analyzers.topic_extraction, text: $input}}\n" + exec, "input.bindings.text"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, "r.yaml", head+c.body), opts(newsEnv))
			require.ErrorContains(t, err, c.want)
		})
	}
	// translation is optional
	_, err := Load(write(t, "r.yaml", head+"    depends_on: [topic_extraction]\n    input: {bindings: {topics: $analyzers.topic_extraction}}\n"+exec), opts(newsEnv))
	require.NoError(t, err)
}

func TestNewsKeysRejectedOnOtherAdapters(t *testing.T) {
	_, err := Load(write(t, "r.yaml", "analyzers:\n  - id: a\n    execution: {adapter: custom, handler: text_stats, num: 3}\n"), opts(nil))
	require.ErrorContains(t, err, "only allowed for adapter news_search")
}

func TestNewsSearchDocsExample(t *testing.T) {
	dir := t.TempDir()
	prompt, err := filepath.Abs("../../config/extractor/prompt.md")
	require.NoError(t, err)
	for _, f := range []string{"topic_extraction.yaml", "propaganda_detection.yaml", "news_search.yaml"} {
		b, err := os.ReadFile("../../docs/examples/" + f)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte(strings.ReplaceAll(string(b), "/etc/orchestrator/extractor/prompt.md", prompt)), 0o600))
	}
	env := map[string]string{"EXTRACTOR_BASE_URL": "http://x/v1", "EXTRACTOR_API_KEY": "k", "EXTRACTOR_MODEL": "m",
		"MISTRAL_API_KEY": "mk", "PROPAGANDA_DETECTION_URL": "http://svc", "SERPER_BASE_URL": "https://google.serper.dev", "SERPER_API_KEY": "sk"}
	r, err := Load(dir, opts(env))
	require.NoError(t, err)
	a := r.Get("news_search")
	require.Equal(t, []string{"topic_extraction", "translation"}, a.DependsOn)
	require.Equal(t, "https://google.serper.dev", a.Execution.BaseURL)
	require.Equal(t, "mk", a.Execution.News.Translate.APIKey)
	require.Equal(t, 12, a.Execution.News.MaxResults)
	require.True(t, a.LLM.Include)
	delete(env, "SERPER_API_KEY")
	_, err = Load(dir, opts(env))
	require.ErrorContains(t, err, "SERPER_API_KEY")
}
