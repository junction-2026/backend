package registry

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// tcYAML: the three dependencies (stand-ins) plus a term_context analyzer with the given execution tail.
func tcYAML(deps, exec string) string {
	return "analyzers:\n  - id: topic_extraction\n" + custom + "  - id: translation\n" + custom + "  - id: news_search\n" + custom +
		"  - id: term_context\n    category: coverage\n    depends_on: " + deps + "\n" +
		"    input: {bindings: {extraction: $analyzers.topic_extraction, translation: $analyzers.translation, news: $analyzers.news_search}}\n" +
		"    execution:\n      adapter: term_context\n      base_url: ${B}\n      api_key: ${K}\n      model: ${M}\n      user_agent: ${UA}\n" + exec
}

var tcEnv = map[string]string{"B": "http://llm/v1", "K": "key", "M": "m", "UA": "mailto:ops@example.org"}

const tcDeps = "[topic_extraction, translation, news_search]"

func TestTermContextDefaults(t *testing.T) {
	r, err := Load(write(t, "r.yaml", tcYAML(tcDeps, "")), opts(tcEnv)) // no prompt file is read
	require.NoError(t, err)
	e := r.Get("term_context").Execution
	require.Equal(t, 5, e.MaxTermsN())
	require.Equal(t, 300, e.InPostMaxCharsN())
	require.Equal(t, 400, e.QuoteMaxCharsN())
	require.Equal(t, 3, e.ElsewhereMaxN())
	require.Equal(t, 60*time.Second, e.Timeout)
	require.Equal(t, 168*time.Hour, e.Wiki.CacheTTL)
	require.Equal(t, 2, e.Wiki.MaxConcurrency)
	require.Equal(t, 1000, e.MaxTokens)
	require.Equal(t, FormatJSONSchema, e.ResponseFormat)
	require.Equal(t, "mailto:ops@example.org", e.Wiki.UserAgent)
	require.Equal(t, DefaultWikipediaBaseURLTemplate, e.Wiki.BaseURLTemplate)

	r, err = Load(write(t, "r.yaml", tcYAML(tcDeps, "      max_terms: 2\n      in_post_max_chars: 100\n      quote_max_chars: 200\n"+
		"      elsewhere_max: 1\n      cache_ttl: 1h\n      max_concurrency: 4\n      timeout: 90s\n")), opts(tcEnv))
	require.NoError(t, err)
	e = r.Get("term_context").Execution
	require.Equal(t, []int{2, 100, 200, 1}, []int{e.MaxTermsN(), e.InPostMaxCharsN(), e.QuoteMaxCharsN(), e.ElsewhereMaxN()})
	require.Equal(t, time.Hour, e.Wiki.CacheTTL)
	require.Equal(t, 4, e.Wiki.MaxConcurrency)
	require.Equal(t, 90*time.Second, e.Timeout)
}

func TestTermContextDisabledNeedsNoSecrets(t *testing.T) {
	_, err := Load(write(t, "r.yaml", tcYAML(tcDeps, "      timeout: 5s\n")), opts(nil)) // still enabled: ${B} is unset
	require.ErrorContains(t, err, "environment variable")
	y := tcYAML(tcDeps, "")
	r, err := Load(write(t, "r.yaml", y+"    enabled: false\n"), opts(nil))
	require.NoError(t, err)
	require.False(t, r.Get("term_context").Enabled)
}

func TestTermContextValidation(t *testing.T) {
	for _, c := range []struct{ deps, tail, msg string }{
		{"[topic_extraction, translation]", "", "depends_on must include news_search"},
		{"[topic_extraction, news_search]", "", "depends_on must include translation"},
		{"[translation, news_search]", "", "depends_on must include topic_extraction"},
		{tcDeps, "      max_terms: 0\n", "execution.max_terms must be a positive integer"},
		{tcDeps, "      in_post_max_chars: -1\n", "execution.in_post_max_chars must be a positive integer"},
		{tcDeps, "      quote_max_chars: 0\n", "execution.quote_max_chars must be a positive integer"},
		{tcDeps, "      elsewhere_max: 0\n", "execution.elsewhere_max must be a positive integer"},
		{tcDeps, "      max_concurrency: 0\n", "execution.max_concurrency must be between 1 and 20"},
		{tcDeps, "      prompt_file: /x\n", "are allowed for adapter term_context"},
		{tcDeps, "      min_check_worthiness: 0.5\n", "are allowed for adapter term_context"},
		{tcDeps, "      max_hits: 3\n", "are allowed for adapter term_context"},
		{tcDeps, "      base_url_template: https://x/w/api.php\n", "must contain {lang}"},
		{tcDeps, "      extra_body: {model: x}\n", "execution.extra_body.model is not allowed"},
	} {
		_, err := Load(write(t, "r.yaml", tcYAML(c.deps, c.tail)), opts(tcEnv))
		require.ErrorContains(t, err, c.msg, c.tail)
	}
	_, err := Load(write(t, "r.yaml", tcYAML(tcDeps, "")+"    trigger: after_all_analyzers_settled\n"), opts(tcEnv))
	require.Error(t, err)
	_, err = Load(write(t, "r.yaml", "analyzers:\n  - id: topic_extraction\n"+custom+"    execution: {max_terms: 3}\n"), opts(tcEnv))
	require.Error(t, err)
}

func TestTermContextBindings(t *testing.T) {
	y := tcYAML(tcDeps, "")
	bad := "input: {bindings: {extraction: $analyzers.topic_extraction, translation: $analyzers.translation}}"
	require.Contains(t, y, "news: $analyzers.news_search")
	y = strings.Replace(y, "input: {bindings: {extraction: $analyzers.topic_extraction, translation: $analyzers.translation, news: $analyzers.news_search}}", bad, 1)
	_, err := Load(write(t, "r.yaml", y), opts(tcEnv))
	require.ErrorContains(t, err, "must be exactly extraction, translation and news")
}

func TestTermContextExampleLoads(t *testing.T) {
	b, err := os.ReadFile("../../docs/examples/term_context.yaml")
	require.NoError(t, err)
	env := map[string]string{"TERM_CONTEXT_BASE_URL": "http://llm/v1", "TERM_CONTEXT_API_KEY": "k", "TERM_CONTEXT_MODEL": "m", "WIKIPEDIA_CONTACT": "mailto:ops@example.org"}
	r, err := Load(write(t, "r.yaml", string(b)+"\n  - id: topic_extraction\n"+custom+"  - id: translation\n"+custom+"  - id: news_search\n"+custom), opts(env))
	require.NoError(t, err)
	a := r.Get("term_context")
	require.Equal(t, "coverage", a.Category)
	require.Equal(t, TriggerOnDeps, a.Trigger)
	require.Equal(t, []string{"topic_extraction", "translation", "news_search"}, a.DependsOn)
	require.Equal(t, []int{5, 300, 400, 3}, []int{a.Execution.MaxTermsN(), a.Execution.InPostMaxCharsN(), a.Execution.QuoteMaxCharsN(), a.Execution.ElsewhereMaxN()})
	require.Nil(t, a.LLM)
}
