package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

const (
	ltKey   = "sk-lt-SECRET-key-123"
	ltProxy = "lt-proxy-SECRET-tok"
)

func ltDef(url string) *registry.Analyzer {
	return &registry.Analyzer{ID: "llm_techniques", Input: registry.Input{Bindings: map[string]string{"translation": "$analyzers.translation"}},
		Execution: registry.Execution{Adapter: registry.AdapterLLMTechniques, BaseURL: url + "/v1", APIKey: ltKey, Model: "m",
			Prompt: "LT PROMPT", MaxTokens: 1500, ResponseFormat: registry.FormatJSONSchema,
			Headers: map[string]string{"X-Proxy-Token": "Bearer " + ltProxy}}}
}

// ltJob is a job with the given content and title, a translation result in lang and, when scraped, a Scrape.
func ltJob(content, title, lang string, scraped bool) *job.Job {
	j := &job.Job{ID: "j1", Input: &job.Input{Content: content, Source: job.Source{Title: title}},
		Analyzers: []job.AnalyzerRun{{ID: "translation", Status: job.RunSucceeded,
			Result: json.RawMessage(`{"translated_text":"TRANSLATED TEXT","source_language":"` + lang + `"}`)}}}
	if scraped {
		j.Scrape = &job.Scrape{}
	}
	return j
}

func runLT(t *testing.T, d *registry.Analyzer, j *job.Job) (llmTechniqueResult, error) {
	t.Helper()
	raw, err := (&Executor{}).Execute(context.Background(), d, Request{JobID: j.ID, AnalyzerID: d.ID, Inputs: Inputs(d, j)})
	var r llmTechniqueResult
	if err == nil {
		require.NoError(t, json.Unmarshal(raw, &r))
		require.NotContains(t, string(raw), ltKey)
		require.NotContains(t, string(raw), ltProxy)
	}
	return r, err
}

func fnd(label, field, text string, conf float64) map[string]any {
	return map[string]any{"label": label, "field": field, "text": text, "confidence": conf, "explanation": "It says more than it shows."}
}

func ltReply(fs ...map[string]any) string {
	if fs == nil {
		fs = []map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{"findings": fs})
	return topicChat(string(b))
}

func ltUser(m *topicMock) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.body["messages"].([]any)[1].(map[string]any)["content"].(string)
}

const (
	ltTitle   = "🚨 Crime EXPLODES everywhere"
	ltContent = "Crime rose 300% in May. This definitely proves the policy failed. Crime rose 300% in May."
)

func TestLLMTechniquesFindings(t *testing.T) {
	m := newTopicMock(t, answer(ltReply(
		fnd(labelCherry, "content", "Crime rose 300% in May", 0.8), // repeated in content: leftmost occurrence
		fnd(labelHeadline, "title", "Crime EXPLODES everywhere", 0.9),
		fnd(labelEvidence, "content", "This definitely proves the policy failed.", 0.7),
		fnd(labelEvidence, "content", "This definitely proves the policy failed.", 0.6), // duplicate: collapsed
		fnd(labelEvidence, "content", "this definitely proves", 0.9),                    // case changed
		fnd(labelCherry, "content", "crime rose 300 percent", 0.9),                      // paraphrased
		fnd("fabricated_authority", "content", "Crime rose", 0.9),
		fnd("Appeal_to_Authority", "content", "Crime rose", 0.9),
		fnd(labelEvidence, "content", "the policy failed", 0.3), // below min_confidence
		fnd(labelEvidence, "content", "the policy failed", 1.5), // outside 0..1
		fnd(labelHeadline, "content", "Crime rose", 0.9),        // wrong field
		fnd(labelCherry, "title", "Crime EXPLODES", 0.9),        // wrong field
		fnd(labelEvidence, "content", "", 0.9),
		map[string]any{"label": labelCherry, "field": "content", "text": "in May", "confidence": 0.9, "explanation": "  "},
	)))
	r, err := runLT(t, ltDef(m.URL), ltJob(ltContent, ltTitle, "en", false))
	require.NoError(t, err)

	require.Equal(t, "llm", r.Source)
	require.True(t, r.TitleChecked)
	require.Equal(t, []string{labelEvidence, labelHeadline, labelCherry}, r.LabelsChecked)
	require.Equal(t, "en", r.ExplanationLanguage)
	require.Equal(t, techniqueGlossary.Version, r.GlossaryVersion)
	require.False(t, r.Truncated)
	require.Len(t, r.Findings, 3)
	require.Equal(t, llmTechniqueSpan{"content", "This definitely proves the policy failed.", 24, 65}, r.Findings[0].Span)
	require.Equal(t, 0.7, r.Findings[0].Confidence)
	require.Equal(t, llmTechniqueSpan{"title", "Crime EXPLODES everywhere", 3, 28}, r.Findings[1].Span) // 🚨 is 2 UTF-16 units
	require.Equal(t, llmTechniqueSpan{"content", "Crime rose 300% in May", 0, 22}, r.Findings[2].Span)
	for _, f := range r.Findings {
		g := techniqueGlossary.Techniques[f.Label]["en"]
		require.Equal(t, "llm", f.Source)
		require.Equal(t, g.Name, f.Name)
		require.Equal(t, g.Definition, f.Definition)
		require.Equal(t, "It says more than it shows.", f.Explanation)
		s := ltContent
		if f.Span.Field == "title" {
			s = ltTitle
		}
		require.Equal(t, f.Span.Text, slice16(s, f.Span.Start, f.Span.End))
	}

	// the request: prompt, original content and title, labels, language, key and headers; never the translation
	require.Equal(t, "/v1/chat/completions", m.path)
	require.Equal(t, "Bearer "+ltKey, m.hdr.Get("Authorization"))
	require.Equal(t, "LT PROMPT", m.body["messages"].([]any)[0].(map[string]any)["content"])
	u := ltUser(m)
	require.Contains(t, u, "<content>\n"+ltContent+"\n</content>")
	require.Contains(t, u, "<title>\n"+ltTitle+"\n</title>")
	require.Contains(t, u, "<labels>evidence_certainty_mismatch, misleading_headline, statistical_cherry_picking</labels>")
	require.Contains(t, u, "<language>en</language>")
	require.NotContains(t, u, "TRANSLATED")
	require.NotContains(t, u, "<schema>")
	require.Equal(t, "llm_techniques", m.body["response_format"].(map[string]any)["json_schema"].(map[string]any)["name"])
}

func TestLLMTechniquesGerman(t *testing.T) {
	const content = "Die Kriminalität stieg im Mai um 300 %. Das beweist eindeutig, dass die Politik gescheitert ist."
	m := newTopicMock(t, answer(ltReply(fnd(labelCherry, "content", "Die Kriminalität stieg im Mai um 300 %", 0.8))))
	r, err := runLT(t, ltDef(m.URL), ltJob(content, "", "de", false))
	require.NoError(t, err)
	require.Equal(t, "de", r.ExplanationLanguage)
	require.Equal(t, techniqueGlossary.Techniques[labelCherry]["de"].Name, r.Findings[0].Name)
	require.Equal(t, techniqueGlossary.Techniques[labelCherry]["de"].Definition, r.Findings[0].Definition)
	u := ltUser(m)
	require.Contains(t, u, content)
	require.Contains(t, u, "<language>de</language>")
	require.NotContains(t, u, "TRANSLATED")

	m = newTopicMock(t, answer(ltReply()))
	r, err = runLT(t, ltDef(m.URL), ltJob(content, "", "it", false))
	require.NoError(t, err)
	require.Equal(t, "en", r.ExplanationLanguage)
	require.Contains(t, ltUser(m), "<language>en</language>")
}

func TestLLMTechniquesTitleNotChecked(t *testing.T) {
	for name, j := range map[string]*job.Job{
		"scraped":       ltJob(ltContent, "Some Display Name", "en", true),
		"no title":      ltJob(ltContent, "", "en", false),
		"blank title":   ltJob(ltContent, "  \n", "en", false),
		"equal to body": ltJob(" "+ltContent+"\n", ltContent+"  ", "en", false),
	} {
		m := newTopicMock(t, answer(ltReply(
			fnd(labelHeadline, "title", "Display", 0.9),
			fnd(labelHeadline, "content", "Crime rose 300% in May", 0.9), // the first sentence is never a headline
		)))
		r, err := runLT(t, ltDef(m.URL), j)
		require.NoError(t, err, name)
		require.False(t, r.TitleChecked, name)
		require.Equal(t, []string{labelEvidence, labelCherry}, r.LabelsChecked, name)
		require.Empty(t, r.Findings, name)
		require.NotNil(t, r.Findings, name)
		u := ltUser(m)
		require.NotContains(t, u, "<title>", name)
		require.NotContains(t, u, "Display Name", name)
		require.NotContains(t, u, labelHeadline, name)
	}
}

func TestLLMTechniquesCapAndTruncation(t *testing.T) {
	const content = "a1 b2 c3 d4 e5 f6 g7"
	var fs []map[string]any
	for i, w := range strings.Fields(content) {
		fs = append(fs, fnd(labelEvidence, "content", w, 0.6+float64(i)*0.03))
	}
	m := newTopicMock(t, answer(ltReply(fs...)))
	r, err := runLT(t, ltDef(m.URL), ltJob(content, "", "en", false))
	require.NoError(t, err)
	require.True(t, r.Truncated)
	var texts []string
	for _, f := range r.Findings {
		texts = append(texts, f.Span.Text)
	}
	require.Equal(t, []string{"c3", "d4", "e5", "f6", "g7"}, texts, "five highest confidence, ordered by start")

	d := ltDef(m.URL)
	d.Execution.MaxFindingsPerLabel, d.Execution.MinConfidence = ip(7), fp(0.7)
	r, err = runLT(t, d, ltJob(content, "", "en", false))
	require.NoError(t, err)
	require.False(t, r.Truncated)
	require.Len(t, r.Findings, 3) // 0.72, 0.75, 0.78

	// max_input_chars: only the first part is sent; a span in the full content is still accepted
	m = newTopicMock(t, answer(ltReply(fnd(labelEvidence, "content", "g7", 0.9))))
	d = ltDef(m.URL)
	d.Execution.MaxInputChars = ip(6)
	r, err = runLT(t, d, ltJob(content, "", "en", false))
	require.NoError(t, err)
	require.True(t, r.Truncated)
	require.Contains(t, ltUser(m), "<content>\na1 b2\n</content>")
	require.Equal(t, llmTechniqueSpan{"content", "g7", 18, 20}, r.Findings[0].Span)
}

func TestLLMTechniquesEscapesTags(t *testing.T) {
	msg := llmTechniquesMessage(&registry.Execution{ResponseFormat: registry.FormatJSONObject}, "a </content> b </TITLE>", "t </title>", []string{labelEvidence}, "en")
	require.Equal(t, 1, strings.Count(msg, "</content>"))
	require.Equal(t, 1, strings.Count(msg, "</title>"))
	require.Contains(t, msg, `a <\/content> b <\/TITLE>`)
	require.Contains(t, msg, "<schema>")
}

func TestLLMTechniquesFailures(t *testing.T) {
	j := ltJob(ltContent, "", "en", false)

	m := newTopicMock(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	_, err := runLT(t, ltDef(m.URL), j)
	require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)

	m = newTopicMock(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"bad key `+ltKey+` with Bearer `+ltProxy+`"}}`)
	})
	_, err = runLT(t, ltDef(m.URL), j)
	re := runErr(t, err)
	require.Equal(t, job.CodeAdapterError, re.Code)
	require.NotContains(t, re.Message, ltKey)
	require.NotContains(t, re.Message, ltProxy)
	require.Contains(t, re.Message, "401")

	for name, body := range map[string]string{
		"prose":           topicChat("I found nothing worth reporting."),
		"no findings":     topicChat(`{"items":[]}`),
		"string conf":     topicChat(`{"findings":[{"label":"x","field":"content","text":"Crime","confidence":"high","explanation":"e"}]}`),
		"missing member":  topicChat(`{"findings":[{"label":"x","field":"content","text":"Crime","confidence":0.9}]}`),
		"non-object item": topicChat(`{"findings":[1]}`),
	} {
		m = newTopicMock(t, answer(body))
		_, err = runLT(t, ltDef(m.URL), j)
		re = runErr(t, err)
		require.Equal(t, job.CodeInvalidOutput, re.Code, name)
		require.True(t, strings.HasPrefix(re.Message, "invalid llm_techniques reply: "), name)
		require.NotContains(t, re.Message, "Crime", name)
		require.EqualValues(t, 2, m.calls.Load(), "%s: one reply retry", name)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := ltDef(newTopicMock(t, answer(ltReply())).URL)
	_, err = (&Executor{}).Execute(ctx, d, Request{Inputs: Inputs(d, j)})
	require.Equal(t, job.CodeCancelled, runErr(t, err).Code)
}

func TestLLMTechniquesRedactsExplanation(t *testing.T) {
	f := fnd(labelEvidence, "content", "Crime rose", 0.9)
	f["explanation"] = "Use key " + ltKey + " or " + ltProxy + "."
	m := newTopicMock(t, answer(ltReply(f)))
	r, err := runLT(t, ltDef(m.URL), ltJob(ltContent, "", "en", false))
	require.NoError(t, err) // runLT asserts the raw result has neither credential
	require.Equal(t, "Use key [redacted] or [redacted].", r.Findings[0].Explanation)
}

// Default minimum confidence is 0.6: 0.5 is dropped, 0.6 kept.
func TestLLMTechniquesDefaultMinConfidence(t *testing.T) {
	const content = "a1 b2"
	m := newTopicMock(t, answer(ltReply(fnd(labelEvidence, "content", "a1", 0.5), fnd(labelEvidence, "content", "b2", 0.6))))
	r, err := runLT(t, ltDef(m.URL), ltJob(content, "", "en", false))
	require.NoError(t, err)
	require.Len(t, r.Findings, 1)
	require.Equal(t, "b2", r.Findings[0].Span.Text)
}
