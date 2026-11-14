package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/registry"
)

// glossaryHashes pins the artifact's content per version: reword anything and you must bump `version` and add
// the new hash here (never edit an existing pair).
var glossaryHashes = map[string]string{
	"2026-11-14.1": "7b786c24a4ece1c9fa69cfa0b51acf3e065fd3c726aed0e8a73e1e8253f6d149",
}

func TestGlossaryVersionPinned(t *testing.T) {
	sum := sha256.Sum256(glossaryYAML)
	require.Equal(t, glossaryHashes[techniqueGlossary.Version], hex.EncodeToString(sum[:]),
		"technique_glossary.yaml changed: bump its version and pin the new hash")
}

func TestGlossaryComplete(t *testing.T) {
	all := append(append([]string{}, registry.TechniqueNames...), llmTechniqueLabels...)
	require.Len(t, all, 21)
	for _, l := range glossaryLanguages {
		tp := techniqueGlossary.Templates[l]
		require.Equal(t, 1, strings.Count(tp.WhySpan, "{name}"), l)
		require.Equal(t, 1, strings.Count(tp.WhySpan, "{span}"), l)
		require.Equal(t, 1, strings.Count(tp.WhyNoSpan, "{name}"), l)
		require.NotContains(t, tp.WhyNoSpan, "{span}", l)
		for _, n := range all {
			e := techniqueGlossary.Techniques[n][l]
			require.NotEmpty(t, strings.TrimSpace(e.Name), "%s %s", n, l)
			require.NotEmpty(t, strings.TrimSpace(e.Definition), "%s %s", n, l)
		}
	}
	require.Equal(t, "Loaded language", techniqueGlossary.Techniques["Loaded_Language"]["en"].Name)
	require.Contains(t, techniqueGlossary.Techniques["Loaded_Language"]["en"].Definition, "strong emotional implications")
}

func TestGlossaryRejectsBrokenArtifact(t *testing.T) {
	src := string(glossaryYAML)
	for name, c := range map[string]struct{ old, new, msg string }{
		"missing language": {`    uk: {name: "Сіяння сумнівів"`, `    xx: {name: "Сіяння сумнівів"`, "techniques.Doubt.uk"},
		"two names":        {`why_span: "The phrase “{span}” is marked as {name}."`, `why_span: "{name}: “{span}” is marked as {name}."`, "templates.en.why_span"},
		"span in no-span":  {`why_no_span: "{name} was indicated`, `why_no_span: "{name} {span} was indicated`, "templates.en.why_no_span"},
		"empty definition": {`definition: "Questions the credibility of someone or something."`, `definition: " "`, "techniques.Doubt.en"},
		"unknown entry":    {"  Doubt:\n", "  Fabricated:\n    en: {name: x, definition: y}\n  Doubt:\n", "unknown technique Fabricated"},
		"no version":       {`version: "2026-11-14.1"`, `version: ""`, "version is empty"},
	} {
		require.Contains(t, src, c.old, name)
		_, err := parseGlossary([]byte(strings.Replace(src, c.old, c.new, 1)))
		require.ErrorContains(t, err, c.msg, name)
	}
}

func TestExplanationLanguage(t *testing.T) {
	for in, want := range map[string]string{"de": "de", "DE": "de", "uk": "uk", "it": "en", "": "en", "zz-top": "en"} {
		require.Equal(t, want, explanationLanguage(in), in)
	}
}

// plainResult is the plain-language part of a TechniqueClassification.
type plainResult struct {
	ExplanationLanguage string `json:"explanation_language"`
	GlossaryVersion     string `json:"glossary_version"`
	SpanStatus          string `json:"span_status"`
	Detected            []struct {
		Name          string          `json:"name"`
		PlainLanguage plainLanguage   `json:"plain_language"`
		Spans         []techniqueSpan `json:"spans"`
	} `json:"detected"`
}

func runPlain(t *testing.T, d *registry.Analyzer, r Request) plainResult {
	t.Helper()
	raw, err := (&Executor{}).Execute(context.Background(), d, r)
	require.NoError(t, err)
	var res plainResult
	require.NoError(t, json.Unmarshal(raw, &res))
	require.Equal(t, techniqueGlossary.Version, res.GlossaryVersion)
	return res
}

func TestTechniquePlainLanguageGerman(t *testing.T) {
	const content = "Sie LÜGEN euch an. 😡 Verräter! Glaubt ihnen nichts."
	llm := newTopicMock(t, answer(phrasesReply([2]string{ll, "LÜGEN euch"}, [2]string{ll, "Verräter"})))
	sent, doc := newClassifierStub(t), docStub(t)
	r := runPlain(t, spanDef(doc.URL, sent.URL, llm.URL), techReq("They are LYING to you.", "de", content))

	require.Equal(t, "de", r.ExplanationLanguage)
	de := techniqueGlossary.Techniques[ll]["de"]
	d := r.Detected[0]
	require.Equal(t, ll, d.Name)
	require.Equal(t, de.Name, d.PlainLanguage.Name)
	require.Equal(t, de.Definition, d.PlainLanguage.Definition)
	require.Len(t, d.Spans, 2)
	require.Equal(t, "Die Stelle „LÜGEN euch“ wurde als Emotional aufgeladene Sprache markiert.", d.Spans[0].Why)
	require.Equal(t, "Die Stelle „Verräter“ wurde als Emotional aufgeladene Sprache markiert.", d.Spans[1].Why)
	require.Equal(t, d.Spans[0].Why, d.PlainLanguage.Why)
	// nc has no span: the no-span sentence, quoting nothing from the content
	n := r.Detected[1]
	require.Empty(t, n.Spans)
	require.Equal(t, strings.Replace(techniqueGlossary.Templates["de"].WhyNoSpan, "{name}", techniqueGlossary.Techniques[nc]["de"].Name, 1), n.PlainLanguage.Why)
	require.EqualValues(t, 1, llm.calls.Load(), "the only LLM request is the span request; no LLM writes names, definitions or why")
}

func TestTechniquePlainLanguageEnglish(t *testing.T) {
	const content = "Wake up now 😡! They are LYING to you."
	sent := newClassifierStub(t)
	sent.scoreFn = func(s string) map[string]float64 { return map[string]float64{ll: 0.9} }
	llm := newTopicMock(t, answer(phrasesReply()))
	doc := docStub(t)
	r := runPlain(t, spanDef(doc.URL, sent.URL, llm.URL), techReq("Wake up now! They are LYING to you.", "en", content))
	require.Equal(t, "en", r.ExplanationLanguage)
	d := r.Detected[0]
	require.Equal(t, "Loaded language", d.PlainLanguage.Name)
	require.Equal(t, `The phrase “Wake up now 😡!” is marked as Loaded language.`, d.Spans[0].Why)
	require.Equal(t, d.Spans[0].Why, d.PlainLanguage.Why)
	require.Zero(t, llm.calls.Load(), "sentence method: no LLM request at all")
}

func TestTechniquePlainLanguageFallbackAndNoSpan(t *testing.T) {
	doc := docStub(t)
	for _, lang := range []string{"it", ""} {
		r := runPlain(t, techDef(doc.URL, doc.URL), techReq(nWords(10), lang, "Some original text here."))
		require.Equal(t, "en", r.ExplanationLanguage)
		d := r.Detected[0]
		require.Equal(t, techniqueGlossary.Techniques[ll]["en"].Name, d.PlainLanguage.Name)
		require.Equal(t, techniqueGlossary.Techniques[ll]["en"].Definition, d.PlainLanguage.Definition)
		require.Equal(t, "Loaded language was indicated by the score of the whole text; no specific phrase was located.", d.PlainLanguage.Why)
		require.NotContains(t, d.PlainLanguage.Why, "Some original")
	}
}

func TestTechniquePlainLanguageSpanFailed(t *testing.T) {
	llm := newTopicMock(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) })
	sent, doc := newClassifierStub(t), docStub(t)
	r := runPlain(t, spanDef(doc.URL, sent.URL, llm.URL), techReq("They lie.", "fi", "He valehtelevat teille."))
	require.Equal(t, "failed", r.SpanStatus)
	require.Equal(t, "fi", r.ExplanationLanguage)
	for _, d := range r.Detected {
		require.Empty(t, d.Spans)
		require.Equal(t, strings.Replace(techniqueGlossary.Templates["fi"].WhyNoSpan, "{name}", techniqueGlossary.Techniques[d.Name]["fi"].Name, 1), d.PlainLanguage.Why)
	}
}

func TestExplainLiteralSubstitution(t *testing.T) {
	d := detectedTechnique{techniqueDetection: techniqueDetection{Name: "Doubt"},
		Spans: []techniqueSpan{{Technique: "Doubt", Text: "say {name} now {span}"}}}
	explain(&d, "en")
	require.Equal(t, "The phrase “say {name} now {span}” is marked as Doubt.", d.Spans[0].Why)
	require.Equal(t, d.Spans[0].Why, d.PlainLanguage.Why)
}
