package registry

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func wikiYAML(exec string) string {
	return "analyzers:\n  - id: topic_extraction\n" + custom + "  - id: translation\n" + custom +
		"  - id: wikipedia\n    depends_on: [topic_extraction, translation]\n" +
		"    input: {bindings: {extraction: $analyzers.topic_extraction, translation: $analyzers.translation}}\n" +
		"    execution:\n      adapter: wikipedia\n" + exec
}

const wikiOK = "      user_agent: ${WC}\n"

var wikiEnv = map[string]string{"WC": "mailto:ops@example.org"}

func TestWikipediaDefaults(t *testing.T) {
	r, err := Load(write(t, "r.yaml", wikiYAML(wikiOK)), opts(wikiEnv))
	require.NoError(t, err)
	e := r.Get("wikipedia").Execution
	require.Equal(t, 20*time.Second, e.Timeout)
	require.Equal(t, Wikipedia{MinCheckWorthiness: 0.4, MaxClaims: 3, MaxHits: 5, MaxPassageChars: 1500, MaxConcurrency: 2,
		CacheDir: filepath.Join("var", "wikipedia"), UserAgent: "mailto:ops@example.org", BaseURLTemplate: DefaultWikipediaBaseURLTemplate,
		CacheTTL: 168 * time.Hour}, e.Wiki)
}

func TestWikipediaDataDirAndOverrides(t *testing.T) {
	r, err := Load(write(t, "r.yaml", wikiYAML(wikiOK)), opts(map[string]string{"WC": "c", "DATA_DIR": "/data"}))
	require.NoError(t, err)
	require.Equal(t, "/data/wikipedia", r.Get("wikipedia").Execution.Wiki.CacheDir)

	r, err = Load(write(t, "r.yaml", wikiYAML(wikiOK+"      min_check_worthiness: 0.7\n      max_claims: 2\n      max_hits: 9\n      max_passage_chars: 300\n"+
		"      max_concurrency: 4\n      cache_dir: /tmp/w\n      cache_ttl: 1h\n      base_url_template: http://127.0.0.1:1/{lang}/api\n      timeout: 5s\n")), opts(wikiEnv))
	require.NoError(t, err)
	e := r.Get("wikipedia").Execution
	require.Equal(t, Wikipedia{MinCheckWorthiness: 0.7, MaxClaims: 2, MaxHits: 9, MaxPassageChars: 300, MaxConcurrency: 4, CacheDir: "/tmp/w",
		UserAgent: "mailto:ops@example.org", BaseURLTemplate: "http://127.0.0.1:1/{lang}/api", CacheTTL: time.Hour}, e.Wiki)
	require.Equal(t, 5*time.Second, e.Timeout)
}

func TestWikipediaValidationNamesAnalyzer(t *testing.T) {
	for name, c := range map[string]struct{ exec, want string }{
		"no user_agent":      {"", "execution.user_agent"},
		"blank user_agent":   {"      user_agent: \"  \"\n", "execution.user_agent"},
		"empty var":          {"      user_agent: ${E}\n", "execution.user_agent"},
		"worthiness > 1":     {wikiOK + "      min_check_worthiness: 1.5\n", "execution.min_check_worthiness"},
		"worthiness < 0":     {wikiOK + "      min_check_worthiness: -0.1\n", "execution.min_check_worthiness"},
		"zero claims":        {wikiOK + "      max_claims: 0\n", "execution.max_claims"},
		"zero hits":          {wikiOK + "      max_hits: 0\n", "execution.max_hits"},
		"too many hits":      {wikiOK + "      max_hits: 21\n", "execution.max_hits"},
		"zero passage chars": {wikiOK + "      max_passage_chars: 0\n", "execution.max_passage_chars"},
		"zero concurrency":   {wikiOK + "      max_concurrency: 0\n", "execution.max_concurrency"},
		"zero ttl":           {wikiOK + "      cache_ttl: 0s\n", "execution.cache_ttl"},
		"template no {lang}": {wikiOK + "      base_url_template: http://x/api.php\n", "{lang}"},
		"negative timeout":   {wikiOK + "      timeout: -1s\n", "timeout"},
		"foreign key":        {wikiOK + "      api_key: k\n", "are allowed for adapter wikipedia"},
		"foreign endpoint":   {wikiOK + "      endpoint: http://x\n", "are allowed for adapter wikipedia"},
		"unset var":          {"      user_agent: ${NOPE}\n", "NOPE"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, "r.yaml", wikiYAML(c.exec)), opts(map[string]string{"WC": "c", "E": ""}))
			require.ErrorContains(t, err, c.want)
			require.ErrorContains(t, err, `analyzer "wikipedia"`)
		})
	}
}

func TestWikipediaKeysOnlyForWikipedia(t *testing.T) {
	for _, k := range []string{"max_hits: 3", "max_passage_chars: 9", "cache_dir: /x", "user_agent: c", "base_url_template: 'http://x/{lang}'"} {
		_, err := Load(write(t, "r.yaml", "analyzers:\n  - id: a\n    execution: {adapter: custom, handler: text_stats, "+k+"}\n"), opts(nil))
		require.ErrorContains(t, err, "only allowed for adapter wikipedia", k)
	}
}

func TestWikipediaBindingsAndDependencies(t *testing.T) {
	head := "analyzers:\n  - id: topic_extraction\n" + custom + "  - id: translation\n" + custom + "  - id: wikipedia\n"
	exec := "    execution:\n      adapter: wikipedia\n" + wikiOK
	for name, c := range map[string]struct{ body, want string }{
		"no topic_extraction dep": {"    depends_on: [translation]\n    input: {bindings: {extraction: $analyzers.translation}}\n" + exec, "depends_on must include topic_extraction"},
		"no bindings":             {"    depends_on: [topic_extraction]\n" + exec, "input.bindings.extraction"},
		"unknown binding":         {"    depends_on: [topic_extraction]\n    input: {bindings: {extraction: $analyzers.topic_extraction, text: $input}}\n" + exec, "input.bindings"},
		"translation not in deps": {"    depends_on: [topic_extraction]\n    input: {bindings: {extraction: $analyzers.topic_extraction, translation: $analyzers.translation}}\n" + exec, "not in depends_on"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, "r.yaml", head+c.body), opts(wikiEnv))
			require.ErrorContains(t, err, c.want)
		})
	}
	// translation is optional
	_, err := Load(write(t, "r.yaml", head+"    depends_on: [topic_extraction]\n    input: {bindings: {extraction: $analyzers.topic_extraction}}\n"+exec), opts(wikiEnv))
	require.NoError(t, err)
}

func TestWikipediaExampleLoads(t *testing.T) {
	b, err := os.ReadFile("../../docs/examples/wikipedia.yaml")
	require.NoError(t, err)
	r, err := Load(write(t, "r.yaml", string(b)+"\n  - id: topic_extraction\n"+custom+"  - id: translation\n"+custom), opts(map[string]string{"WIKIPEDIA_CONTACT": "mailto:ops@example.org"}))
	require.NoError(t, err)
	a := r.Get("wikipedia")
	require.Equal(t, "fact_check", a.Category)
	require.Equal(t, []string{"topic_extraction", "translation"}, a.DependsOn)
	require.Equal(t, map[string]string{"extraction": "$analyzers.topic_extraction", "translation": "$analyzers.translation"}, a.Input.Bindings)
	require.True(t, a.LLM.Include)
	require.Contains(t, a.LLM.Description, "not a verdict")
}
