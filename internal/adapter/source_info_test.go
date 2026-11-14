package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/outlets"
	"github.com/junction-2026/backend/internal/registry"
)

const siCSV = "domain,domains,hosts,name,country_code,country,bias,factuality,site_lang\n" +
	"bbc.co.uk,bbc.co.uk|bbc.com,bbc.co.uk|bbc.com,BBC News,GB,United Kingdom,center,veryHigh,en\n" +
	"thehindu.com,thehindu.com,sportstar.thehindu.com,The Hindu,IN,India,leanLeft,high,en\n" +
	"spiegel.de,spiegel.de,,Der Spiegel,DE,Germany,leanLeft,high,de\n" +
	"blog.org,blog.org,,Some Blog,,,unknown,unknown,\n" +
	"fox.com,fox.com,,Fox News,US,United States,right,mixed,en\n"

func siDef(t *testing.T) *registry.Analyzer {
	p := filepath.Join(t.TempDir(), "outlets.csv")
	require.NoError(t, os.WriteFile(p, []byte(siCSV), 0o600))
	tb, err := outlets.Load(p)
	require.NoError(t, err)
	return &registry.Analyzer{ID: "source_info", Execution: registry.Execution{Adapter: registry.AdapterSourceInfo, Outlets: tb}}
}

func siSearch(urls ...string) json.RawMessage {
	arts := make([]map[string]any, len(urls))
	for i, u := range urls {
		arts[i] = map[string]any{"url": u, "title": "t"}
	}
	b, _ := json.Marshal(map[string]any{"articles": arts})
	return b
}

func siRun(t *testing.T, search any, pageURL string) (map[string]any, error) {
	raw, err := (&Executor{}).Execute(context.Background(), siDef(t), Request{AnalyzerID: "source_info",
		Inputs: map[string]any{"search": search, "source": job.Source{URL: pageURL}}})
	if err != nil {
		return nil, err
	}
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out, nil
}

func TestSourceInfoArticles(t *testing.T) {
	out, err := siRun(t, siSearch("https://news.bbc.co.uk/x", "https://sportstar.thehindu.com/x", "https://WWW.BBC.COM./x", "https://unknown.example/x", "::bad", "https://example.org/x"), "")
	require.NoError(t, err)
	arts := out["articles"].([]any)
	require.Len(t, arts, 6)
	a := func(i int) map[string]any { return arts[i].(map[string]any) }
	require.Equal(t, map[string]any{"url": "https://news.bbc.co.uk/x", "domain": "news.bbc.co.uk", "matched": true, "match": "domain",
		"outlet": map[string]any{"name": "BBC News", "domain": "bbc.co.uk", "country_code": "GB", "country": "United Kingdom", "bias": "center", "factuality": "veryHigh"}}, a(0))
	require.Equal(t, "host", a(1)["match"])
	require.Equal(t, "The Hindu", a(1)["outlet"].(map[string]any)["name"])
	require.Equal(t, "bbc.com", a(2)["domain"])
	require.Equal(t, map[string]any{"url": "https://unknown.example/x", "domain": "unknown.example", "matched": false}, a(3))
	require.Equal(t, map[string]any{"url": "::bad", "domain": nil, "matched": false}, a(4))
	require.Nil(t, out["page_outlet"])
	require.Equal(t, map[string]any{"outlets": float64(5), "source": "outlets.csv"}, out["table"])
}

func TestSourceInfoSummary(t *testing.T) {
	urls := []string{"https://bbc.com/1", "https://bbc.com/2", "https://thehindu.com/3", "https://blog.org/4", "https://fox.com/5",
		"https://spiegel.de/6", "https://x.thehindu.com/7"}
	for i := 0; i < 5; i++ {
		urls = append(urls, fmt.Sprintf("https://nobody%d.example/", i))
	}
	out, err := siRun(t, siSearch(urls...), "https://www.spiegel.de/politik/a-1.html")
	require.NoError(t, err)
	require.Len(t, out["articles"], 12)
	require.Equal(t, map[string]any{"articles": float64(12), "matched": float64(7), "outlets": float64(5),
		"bias":      map[string]any{"center": float64(2), "leanLeft": float64(3), "unknown": float64(1), "right": float64(1)},
		"countries": map[string]any{"GB": float64(2), "IN": float64(2), "": float64(1), "US": float64(1), "DE": float64(1)}}, out["summary"])
	require.Equal(t, "spiegel.de", out["page_outlet"].(map[string]any)["domain"]) // not counted in the summary
}

func TestSourceInfoPageOutletWithoutArticles(t *testing.T) {
	out, err := siRun(t, siSearch(), "https://www.spiegel.de/politik/a-1.html")
	require.NoError(t, err)
	require.Equal(t, []any{}, out["articles"])
	require.Equal(t, "Der Spiegel", out["page_outlet"].(map[string]any)["name"])
	s := out["summary"].(map[string]any)
	require.Equal(t, map[string]any{"articles": float64(0), "matched": float64(0), "outlets": float64(0), "bias": map[string]any{}, "countries": map[string]any{}}, s)

	out, err = siRun(t, siSearch(), "https://unmatched.example/a")
	require.NoError(t, err)
	require.Nil(t, out["page_outlet"])
}

func TestSourceInfoNoSources(t *testing.T) {
	for _, src := range []string{"", "  "} {
		_, err := siRun(t, siSearch(), src)
		var skip *Skip
		require.ErrorAs(t, err, &skip)
		require.Equal(t, "no_sources", skip.Reason)
	}
}

func TestSourceInfoMalformedSearch(t *testing.T) {
	for _, v := range []any{nil, json.RawMessage(`{"articles":"x"}`), json.RawMessage(`{"articles":null}`), json.RawMessage(`[]`), json.RawMessage(`{"articles":[1]}`)} {
		_, err := siRun(t, v, "")
		var re *job.RunError
		require.ErrorAs(t, err, &re, "%v", v)
		require.Equal(t, job.CodeAdapterError, re.Code)
	}
}

func TestSourceInfoCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&Executor{}).Execute(ctx, siDef(t), Request{Inputs: map[string]any{"search": siSearch("https://bbc.com/")}})
	var re *job.RunError
	require.ErrorAs(t, err, &re)
	require.Equal(t, job.CodeCancelled, re.Code)
}
