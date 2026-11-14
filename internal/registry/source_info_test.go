package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const siHdr = "domain,domains,hosts,name,country_code,country,bias,factuality,site_lang\n"

func siYAML(exec, rest string) string {
	return newsYAML(newsOK, "") + "  - id: source_info\n    category: coverage\n    depends_on: [news_search]\n" +
		"    input: {bindings: {search: $analyzers.news_search, source: $input.source}}\n" + rest +
		"    execution:\n      adapter: source_info\n" + exec
}

func siFile(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "outlets.csv")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestSourceInfoLoad(t *testing.T) {
	good := siFile(t, siHdr+"bbc.com,bbc.com,,BBC,GB,UK,center,high,en\n")
	r, err := Load(write(t, "r.yaml", siYAML("      outlets_file: "+good+"\n", "")), opts(newsEnv))
	require.NoError(t, err)
	e := r.Get("source_info").Execution
	require.Equal(t, 1, e.Outlets.Count)
	require.Equal(t, "outlets.csv", e.Outlets.Source)

	// two analyzers on one file share the table
	two := siYAML("      outlets_file: "+good+"\n", "") + "  - id: source_info2\n    depends_on: [news_search]\n" +
		"    input: {bindings: {search: $analyzers.news_search}}\n    execution:\n      adapter: source_info\n      outlets_file: " + good + "\n"
	r, err = Load(write(t, "r.yaml", two), opts(newsEnv))
	require.NoError(t, err)
	require.Same(t, r.Get("source_info").Execution.Outlets, r.Get("source_info2").Execution.Outlets)
}

func TestSourceInfoStartupFailures(t *testing.T) {
	dup := siHdr + "a.com,a.com,,A,,,unknown,unknown,\nb.com,b.com|a.com,,B,,,unknown,unknown,\n"
	for name, c := range map[string]struct{ csv, want string }{
		"duplicate":    {dup, "row 3"},
		"wrong header": {"domain,name\n", "row 1"},
		"empty domain": {siHdr + ",a.com,,A,,,unknown,unknown,\n", "row 2"},
		"missing file": {"", "no-such.csv"},
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "no-such.csv")
			if c.csv != "" {
				p = siFile(t, c.csv)
			}
			_, err := Load(write(t, "r.yaml", siYAML("      outlets_file: "+p+"\n", "")), opts(newsEnv))
			require.ErrorContains(t, err, `analyzer "source_info"`)
			require.ErrorContains(t, err, c.want)
		})
	}
	// a disabled analyzer does not read its file
	_, err := Load(write(t, "r.yaml", siYAML("      outlets_file: /no/such/file.csv\n", "    enabled: false\n")), opts(newsEnv))
	require.NoError(t, err)
}

func TestSourceInfoValidation(t *testing.T) {
	good := siFile(t, siHdr+"bbc.com,bbc.com,,BBC,GB,UK,center,high,en\n")
	f := "      outlets_file: " + good + "\n"
	notNews := "analyzers:\n  - id: topic_extraction\n" + custom + "  - id: source_info\n    depends_on: [topic_extraction]\n" +
		"    input: {bindings: {search: $analyzers.topic_extraction}}\n    execution:\n      adapter: source_info\n" + f
	for name, c := range map[string]struct{ yaml, want string }{
		"other key":         {siYAML(f+"      endpoint: http://x\n", ""), "only execution.outlets_file"},
		"news key":          {siYAML(f+"      num: 3\n", ""), "only execution.outlets_file"},
		"no search binding": {newsYAML(newsOK, "") + "  - id: source_info\n    depends_on: [news_search]\n    execution:\n      adapter: source_info\n" + f, "input.bindings.search"},
		"other binding":     {newsYAML(newsOK, "") + "  - id: source_info\n    depends_on: [news_search, topic_extraction]\n    input: {bindings: {search: $analyzers.news_search, x: $analyzers.topic_extraction}}\n    execution:\n      adapter: source_info\n" + f, "accept only"},
		"not in depends_on": {newsYAML(newsOK, "") + "  - id: source_info\n    input: {bindings: {search: $analyzers.news_search}}\n    execution:\n      adapter: source_info\n" + f, "depends_on"},
		"not news_search":   {notNews, "adapter news_search"},
		"wrong trigger":     {siYAML(f, "    trigger: after_all_analyzers_settled\n"), "trigger"},
		"on other adapter":  {"analyzers:\n  - id: a\n    execution: {adapter: custom, handler: text_stats, outlets_file: x}\n", "only allowed for adapter source_info"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, "r.yaml", c.yaml), opts(newsEnv))
			require.ErrorContains(t, err, c.want)
		})
	}
}

func TestSourceInfoDefaultFile(t *testing.T) {
	r, err := Load(write(t, "r.yaml", siYAML("", "    enabled: false\n")), opts(newsEnv))
	require.NoError(t, err)
	require.Equal(t, DefaultOutletsFile, r.Get("source_info").Execution.OutletsFile)
}

func TestSourceInfoExample(t *testing.T) {
	csv, err := filepath.Abs("../../data/outlets.csv")
	require.NoError(t, err)
	b, err := os.ReadFile("../../docs/examples/source_info.yaml")
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "news.yaml"), []byte(newsYAML(newsOK, "")), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "si.yaml"), []byte(strings.ReplaceAll(string(b), DefaultOutletsFile, csv)), 0o600))
	r, err := Load(dir, opts(newsEnv))
	require.NoError(t, err)
	a := r.Get("source_info")
	require.Greater(t, a.Execution.Outlets.Count, 5000)
	require.True(t, a.LLM.Include)
	require.Contains(t, a.LLM.Description, "never as evidence")
}
