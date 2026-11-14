package finalizer

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

const merkelClaim = "Angela Merkel became chancellor in 2006."

// wikiResult is a wikipedia AnalyzerRun result with one found en lookup per coverage label given.
const wikiResult = `{"languages":["de","en"],"claims":[{"claim":"` + merkelClaim + `","wikis":[
 {"language":"en","status":"found","title":"Angela Merkel","url":"https://en.wikipedia.org/w/index.php?title=Angela_Merkel&oldid=1250000000",
  "revision_id":1250000000,"passage":"Angela Merkel was Chancellor of Germany from 2005 to 2021. She led the CDU.",
  "coverage":"conflicts","evidence_sentence":"Angela Merkel was Chancellor of Germany from 2005 to 2021."},
 {"language":"de","status":"found","title":"Angela Merkel","url":"https://de.wikipedia.org/w/index.php?title=Angela_Merkel&oldid=1",
  "revision_id":1,"passage":"Angela Merkel ist eine Politikerin.","coverage":"no_coverage","evidence_sentence":null}]},
 {"claim":"Unverifiable.","wikis":[{"language":"en","status":"found","title":"T","url":"https://en.wikipedia.org/w/index.php?title=T&oldid=2",
  "revision_id":2,"passage":"Some text 2005.","coverage":"aligned","evidence_sentence":"not in passage"}]}]}`

const quoteAnalyzers = "  - id: topic_extraction\n    execution: {adapter: custom, handler: ok}\n  - id: translation\n    execution: {adapter: custom, handler: ok}\n" +
	"  - id: wikipedia\n    depends_on: [topic_extraction, translation]\n" +
	"    input: {bindings: {extraction: $analyzers.topic_extraction, translation: $analyzers.translation}}\n" +
	"    execution: {adapter: wikipedia, user_agent: mailto:ops@example.org}\n    llm: {include: true, description: d}\n"

func quotesRegistry(t *testing.T, extra string, env map[string]string) *registry.Registry {
	p := filepath.Join(t.TempDir(), "r.yaml")
	require.NoError(t, os.WriteFile(p, []byte("analyzers:\n"+quoteAnalyzers+extra), 0o600))
	reg, err := registry.Load(p, registry.Options{HandlerExists: func(string) bool { return true },
		Getenv: func(k string) (string, bool) { v, ok := env[k]; return v, ok }})
	require.NoError(t, err)
	return reg
}

func quotesRuntime(t *testing.T, log *slog.Logger) *Runtime {
	return &Runtime{Reg: quotesRegistry(t, "", nil), Log: log}
}

func wikiJob(status string) *job.Job {
	return &job.Job{ID: "j1", Analyzers: []job.AnalyzerRun{{ID: "wikipedia", Status: status, Result: json.RawMessage(wikiResult)}}}
}

func TestWikipediaQuotesVerification(t *testing.T) {
	var logs bytes.Buffer
	r := quotesRuntime(t, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	q := func(claim, lang, quote string) string {
		b, _ := json.Marshal(map[string]string{"claim": claim, "language": lang, "quote": quote})
		return string(b)
	}
	out := `{"verdict":"uncertain","user_text":"u","wikipedia_quotes":[` +
		q(merkelClaim, "en", " Chancellor of Germany from 2005 to 2021 ") + "," + // kept, trimmed
		q(merkelClaim, "en", "Merkel took office in 2005") + "," + // paraphrase
		q(merkelClaim, "de", "Angela Merkel ist") + "," + // no_coverage entry
		q(merkelClaim, "fr", "Angela Merkel") + "," + // no such language
		q("Other claim.", "en", "Angela Merkel") + "," + // no such claim
		q(merkelClaim, "en", "   ") + "," + // empty
		q("Unverifiable.", "en", "Some text") + "," + // aligned without a verifiable span counts as mentioned
		`"not an object",{"claim":"x","language":"en"},{"claim":1,"language":"en","quote":"q"},null]}`
	got := r.wikipediaQuotes(wikiJob(job.RunSucceeded), json.RawMessage(out))
	require.Equal(t, []job.WikipediaQuote{
		{Claim: merkelClaim, Language: "en", Quote: "Chancellor of Germany from 2005 to 2021", Title: "Angela Merkel",
			URL: "https://en.wikipedia.org/w/index.php?title=Angela_Merkel&oldid=1250000000", RevisionID: 1250000000, Coverage: "conflicts"},
		{Claim: "Unverifiable.", Language: "en", Quote: "Some text", Title: "T", URL: "https://en.wikipedia.org/w/index.php?title=T&oldid=2",
			RevisionID: 2, Coverage: "mentioned"},
	}, got)
	require.Equal(t, 9, bytes.Count(logs.Bytes(), []byte("wikipedia quote dropped")))
	require.NotContains(t, logs.String(), "took office")
	require.NotContains(t, logs.String(), "Angela")

	// a failed wikipedia run verifies nothing
	require.Equal(t, []job.WikipediaQuote{}, r.wikipediaQuotes(wikiJob(job.RunFailed), json.RawMessage(out)))
}

func TestWikipediaQuotesMalformedMember(t *testing.T) {
	r := quotesRuntime(t, nil)
	for _, out := range []string{`{}`, `{"wikipedia_quotes":null}`, `{"wikipedia_quotes":"see Wikipedia"}`, `{"wikipedia_quotes":{"a":1}}`, `[]`} {
		got := r.wikipediaQuotes(wikiJob(job.RunSucceeded), json.RawMessage(out))
		require.NotNil(t, got, out)
		require.Empty(t, got, out)
	}
}

// Malformed quotes never fail finalization: it completes with the finalizer's verdict and wikipedia_quotes [].
func TestMalformedQuotesDoNotFailFinalization(t *testing.T) {
	s := newStub(t, reply(`{"verdict":"not_detected","user_text":"fine","wikipedia_quotes":"see Wikipedia"}`))
	j, _ := run(t, s, "", req("x"))
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	require.JSONEq(t, `{"verdict":"not_detected","confidence":null,"user_text":"fine","produced_by":"finalizer","usage":null,"attempts":null,"retry_reason":null,"wikipedia_quotes":[]}`,
		string(j.Finalization.Result))
}

// The LLM finalizer receives the wikipedia result unchanged, coverage, evidence_sentence and passage included.
func TestWikipediaLabelsInFinalizerInput(t *testing.T) {
	entries, ok := quotesRuntime(t, nil).llmEntries(wikiJob(job.RunSucceeded))
	require.True(t, ok)
	require.Len(t, entries, 1)
	var e struct{ Result json.RawMessage }
	require.NoError(t, json.Unmarshal(entries[0], &e))
	require.JSONEq(t, wikiResult, string(e.Result))
}

// Both finalizer paths store the verified quotes in finalization.result: the valid quote is kept with the
// WikiLookup's members, the paraphrase is dropped and finalization succeeds with the finalizer's verdict.
func TestWikipediaQuotesThroughFinalization(t *testing.T) {
	out := `{"verdict":"uncertain","confidence":0.5,"user_text":"u","wikipedia_quotes":[` +
		`{"claim":"` + merkelClaim + `","language":"en","quote":" Chancellor of Germany from 2005 to 2021 "},` +
		`{"claim":"` + merkelClaim + `","language":"en","quote":"Merkel took office in 2005"}]}`
	want := `[{"claim":"` + merkelClaim + `","language":"en","quote":"Chancellor of Germany from 2005 to 2021","title":"Angela Merkel",` +
		`"url":"https://en.wikipedia.org/w/index.php?title=Angela_Merkel&oldid=1250000000","revision_id":1250000000,"coverage":"conflicts"}]`
	for name, c := range map[string]struct {
		fin  string
		body string
	}{
		"http_sync": {"  - id: final_synthesis\n    trigger: after_all_analyzers_settled\n" +
			"    execution: {adapter: http_sync, endpoint: \"${URL}\", timeout: 5s}\n", out},
		"llm": {llmFinYAML(""), chat(out)},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStub(t, reply(c.body))
			reg := quotesRegistry(t, c.fin, map[string]string{"URL": s.URL, "FINALIZER_BASE_URL": s.URL + "/v1/",
				"FINALIZER_API_KEY": testKey, "FINALIZER_MODEL": "m", "PROMPT": promptFile(t)})
			j := wikiJob(job.RunSucceeded)
			j.Coverage, j.Input = job.ComputeCoverage(j.Analyzers), &job.Input{Content: "x"}
			raw, err := (&Runtime{Exec: &adapter.Executor{}, Reg: reg}).Run(context.Background(),
				engine.FinalizationInput{Job: *j, Analyzer: reg.Finalizer()})
			require.NoError(t, err)
			require.EqualValues(t, 1, s.calls.Load())
			var a map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &a))
			require.JSONEq(t, `"uncertain"`, string(a["verdict"]))
			require.JSONEq(t, want, string(a["wikipedia_quotes"]))
		})
	}
}
