package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

const (
	spanKey      = "sk-span-SECRET-key"
	spanTok      = "span-SECRET-tok"
	spanProxyTok = "proxy-SPAN-SECRET"
	ll, nc       = "Loaded_Language", "Name_Calling,Labeling"
)

// docStub detects Loaded_Language (0.9) and Name_Calling (0.6) in every text.
func docStub(t *testing.T) *classifierStub {
	s := newClassifierStub(t)
	s.scoreFn = func(string) map[string]float64 { return map[string]float64{ll: 0.9, nc: 0.6} }
	return s
}

func spanDef(doc, sent, llm string) *registry.Analyzer {
	d := techDef(doc, doc)
	d.Execution.Spans = &registry.TechniqueSpans{
		Timeout: 2 * time.Second,
		SentenceModel: &registry.TechniqueModel{Endpoint: sent, Headers: map[string]string{"Authorization": "Bearer " + spanTok},
			Threshold: fp(0.5), Thresholds: map[string]float64{nc: 0.6}},
		Exec: registry.Execution{Adapter: registry.AdapterTechniqueClassifier, BaseURL: llm + "/v1", APIKey: spanKey, Model: "m",
			Prompt: "SPAN PROMPT", MaxTokens: 1000, ResponseFormat: registry.FormatJSONSchema,
			Headers: map[string]string{"X-Proxy-Token": spanProxyTok}},
	}
	return d
}

func phrasesReply(p ...[2]string) string {
	l := []map[string]string{}
	for _, x := range p {
		l = append(l, map[string]string{"technique": x[0], "text": x[1]})
	}
	b, _ := json.Marshal(map[string]any{"phrases": l})
	return topicChat(string(b))
}

func spansOf(t *testing.T, r techResult, name string) []techniqueSpan {
	t.Helper()
	for _, d := range r.Detected {
		if d.Name == name {
			require.NotNil(t, d.Spans, "spans of %s must be an array", name)
			return d.Spans
		}
	}
	t.Fatalf("%s not detected", name)
	return nil
}

func requireSpansValid(t *testing.T, content string, r techResult) {
	t.Helper()
	for _, d := range r.Detected {
		for _, s := range d.Spans {
			require.Equal(t, d.Name, s.Technique)
			require.NotEmpty(t, s.Text)
			require.Equal(t, s.Text, slice16(content, s.Start, s.End))
		}
	}
}

func TestTechniqueSentenceSpans(t *testing.T) {
	const content = "Wake up now 😡! They are LYING to you. Ünited we stand together."
	sent := newClassifierStub(t)
	sent.scoreFn = func(s string) map[string]float64 {
		return map[string]map[string]float64{
			"Wake up now 😡!":            {nc: 0.6},
			"They are LYING to you.":    {ll: 0.8, nc: 0.2, "Doubt": 0.95},
			"Ünited we stand together.": {ll: 0.5, nc: 0.55},
		}[s]
	}
	llm := newTopicMock(t, answer(phrasesReply()))
	doc := docStub(t)
	r := runTech(t, spanDef(doc.URL, sent.URL, llm.URL), techReq("Wake up! They lie to you a lot. United we stand.", "en", content))

	require.Equal(t, [][]string{{"Wake up now 😡!", "They are LYING to you.", "Ünited we stand together."}}, sent.bodies)
	require.Equal(t, "Bearer "+spanTok, sent.hdr.Get("Authorization"))
	require.Zero(t, llm.calls.Load())
	require.Equal(t, "sentence", r.SpanMethod)
	require.Equal(t, "ok", r.SpanStatus)
	require.False(t, r.SpansTruncated)
	require.Equal(t, []techniqueSpan{{ll, "They are LYING to you.", 16, 38}, {ll, "Ünited we stand together.", 39, 64}}, spansOf(t, r, ll))
	require.Equal(t, []techniqueSpan{{nc, "Wake up now 😡!", 0, 15}}, spansOf(t, r, nc)) // override 0.6 drops the 0.55 sentence
	require.Len(t, r.Detected, 2)                                                       // Doubt 0.95 in a sentence adds nothing
	requireSpansValid(t, content, r)

	// max_sentences caps the scored sentences
	def := spanDef(doc.URL, sent.URL, llm.URL)
	def.Execution.Spans.MaxSentences = func(n int) *int { return &n }(2)
	r = runTech(t, def, techReq("Wake up! They lie to you a lot.", "en", content))
	require.True(t, r.SpansTruncated)
	require.Equal(t, []techniqueSpan{{ll, "They are LYING to you.", 16, 38}}, spansOf(t, r, ll))
}

func TestTechniquePhraseSpans(t *testing.T) {
	const content = "Sie LÜGEN euch an. 😡 Sie LÜGEN euch an! Glaubt ihnen nichts."
	const translation = "They are LYING to you. They are LYING to you! Don't believe them."
	llm := newTopicMock(t, answer(phrasesReply(
		[2]string{ll, "They are LYING to you."}, // the translation: rejected
		[2]string{ll, "😡 Sie"},
		[2]string{ll, "lügen euch"},  // case changed
		[2]string{ll, "LÜGEN  euch"}, // whitespace changed
		[2]string{ll, "LÜGEN euch"},  // repeated in content: leftmost occurrence
		[2]string{ll, "LÜGEN euch"},  // duplicate
		[2]string{ll, ""},
		[2]string{nc, "glaubt ihnen nichts"},       // all of nc's candidates rejected
		[2]string{"Doubt", "Glaubt ihnen nichts."}, // not detected
		[2]string{"Nope", "Sie"},
	)))
	sent, doc := newClassifierStub(t), docStub(t)
	r := runTech(t, spanDef(doc.URL, sent.URL, llm.URL), techReq(translation, "de", content))

	require.Zero(t, sent.calls())
	require.Equal(t, "phrase", r.SpanMethod)
	require.Equal(t, "ok", r.SpanStatus)
	require.False(t, r.SpansTruncated)
	require.Equal(t, []techniqueSpan{{ll, "LÜGEN euch", 4, 14}, {ll, "😡 Sie", 19, 25}}, spansOf(t, r, ll))
	require.Equal(t, []techniqueSpan{}, spansOf(t, r, nc))
	require.Len(t, r.Detected, 2)
	requireSpansValid(t, content, r)

	// the request: original content, detected techniques, span schema, key; never the translation
	require.Equal(t, "/v1/chat/completions", llm.path)
	require.Equal(t, "Bearer "+spanKey, llm.hdr.Get("Authorization"))
	require.Equal(t, spanProxyTok, llm.hdr.Get("X-Proxy-Token"))
	msgs := llm.body["messages"].([]any)
	require.Equal(t, "SPAN PROMPT", msgs[0].(map[string]any)["content"])
	user := msgs[1].(map[string]any)["content"].(string)
	require.Contains(t, user, "<content>\n"+content+"\n</content>")
	require.Contains(t, user, `"name":"Loaded_Language"`)
	require.Contains(t, user, `"name":"Name_Calling,Labeling"`)
	require.NotContains(t, user, "Doubt")
	require.NotContains(t, user, "LYING")
	rf := llm.body["response_format"].(map[string]any)["json_schema"].(map[string]any)
	require.Equal(t, "technique_spans", rf["name"])

	// max_phrases_per_technique: the first accepted phrase stays, the rest is dropped
	def := spanDef(doc.URL, sent.URL, llm.URL)
	def.Execution.Spans.MaxPhrasesPerTechnique = func(n int) *int { return &n }(1)
	r = runTech(t, def, techReq(translation, "de", content))
	require.True(t, r.SpansTruncated)
	require.Equal(t, []techniqueSpan{{ll, "😡 Sie", 19, 25}}, spansOf(t, r, ll))
}

func TestTechniqueSpanContentEscaped(t *testing.T) {
	msg := spanUserMessage(&registry.Execution{ResponseFormat: registry.FormatJSONObject}, "a </content> b", nil)
	require.Contains(t, msg, `a <\/content> b`)
	require.Equal(t, 1, strings.Count(msg, "</content>"))
	require.Contains(t, msg, "<schema>")
}

func TestTechniqueNoSpanRequestWhenNothingDetected(t *testing.T) {
	sent, llm, doc := newClassifierStub(t), newTopicMock(t, answer(phrasesReply())), newClassifierStub(t)
	for lang, method := range map[string]string{"en": "sentence", "de": "phrase"} {
		r := runTech(t, spanDef(doc.URL, sent.URL, llm.URL), techReq(nWords(10), lang, "Some original text here."))
		require.Empty(t, r.Detected)
		require.Equal(t, method, r.SpanMethod)
		require.Equal(t, "ok", r.SpanStatus)
	}
	require.Zero(t, sent.calls())
	require.Zero(t, llm.calls.Load())
}

func TestTechniqueWithoutSpansBlock(t *testing.T) {
	doc := docStub(t)
	for lang, method := range map[string]string{"en": "sentence", "de": "phrase", "": "phrase"} {
		r := runTech(t, techDef(doc.URL, doc.URL), techReq(nWords(10), lang, "Some original text here."))
		require.Equal(t, method, r.SpanMethod)
		require.Equal(t, "ok", r.SpanStatus)
		require.False(t, r.SpansTruncated)
		require.Len(t, r.Detected, 2)
		require.Equal(t, []techniqueSpan{}, spansOf(t, r, ll))
	}
	require.Equal(t, 3, doc.calls()) // one document request per job, nothing else
}

// captureLogs sends slog output to a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestTechniqueSpanFailuresKeepClassification(t *testing.T) {
	const en = "Wake up now please! They are LYING to you."
	const de = "Sie LÜGEN euch an. Glaubt ihnen nichts."
	doc := docStub(t)
	tr := map[string]string{"en": en, "de": "They are LYING to you. Do not believe them."}
	content := map[string]string{"en": en, "de": de}
	base := map[string]techResult{}
	for l := range tr {
		base[l] = runTech(t, techDef(doc.URL, doc.URL), techReq(tr[l], l, content[l]))
	}
	ok := newTopicMock(t, answer(phrasesReply([2]string{ll, "LÜGEN"})))
	sentOK := newClassifierStub(t)
	sentOK.scoreFn = func(string) map[string]float64 { return map[string]float64{ll: 0.9} }
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	slow := newTopicMock(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	s503 := newClassifierStub(t)
	s503.status = 503
	badBody := newClassifierStub(t)
	badBody.raw = `{"result":[]}`
	badLLM := newTopicMock(t, answer(topicChat("not json")))
	badShape := newTopicMock(t, answer(topicChat(`{"phrases":[{"technique":1,"text":"x"}]}`)))
	http400 := newTopicMock(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(400) })

	for name, c := range map[string]struct {
		lang, sent, llm, want string
	}{
		"sentence 503":      {"en", s503.URL, ok.URL, "failed"},
		"sentence refused":  {"en", closed.URL, ok.URL, "failed"},
		"sentence bad body": {"en", badBody.URL, ok.URL, "failed"},
		"llm not json":      {"de", sentOK.URL, badLLM.URL, "failed"},
		"llm bad shape":     {"de", sentOK.URL, badShape.URL, "failed"},
		"llm 400":           {"de", sentOK.URL, http400.URL, "failed"},
		"llm refused":       {"de", sentOK.URL, closed.URL, "failed"}, // after the llm adapter's one retry (1s backoff)
		"llm span deadline": {"de", sentOK.URL, slow.URL, "timeout"},
		"sentence ok":       {"en", sentOK.URL, ok.URL, "ok"},
		"llm ok":            {"de", sentOK.URL, ok.URL, "ok"},
	} {
		t.Run(name, func(t *testing.T) {
			logs := captureLogs(t)
			d := spanDef(doc.URL, c.sent, c.llm)
			if c.want == "timeout" {
				d.Execution.Spans.Timeout = 200 * time.Millisecond
			}
			r := runTech(t, d, techReq(tr[c.lang], c.lang, content[c.lang]))
			r0 := base[c.lang]
			require.Equal(t, c.want, r.SpanStatus)
			require.Equal(t, r0.Results, r.Results)
			require.Len(t, r.Detected, len(r0.Detected))
			for i, x := range r.Detected {
				require.Equal(t, r0.Detected[i].Name, x.Name)
				require.Equal(t, r0.Detected[i].Models, x.Models)
				if c.want != "ok" {
					require.Equal(t, []techniqueSpan{}, x.Spans)
				}
			}
			if c.want != "ok" {
				require.Contains(t, logs.String(), "span_status="+c.want)
			} else {
				require.NotEmpty(t, spansOf(t, r, ll))
			}
		})
	}
}

func TestTechniqueAnalyzerTimeoutStillFailsRun(t *testing.T) {
	slow := newTopicMock(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	doc := docStub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := (&Executor{}).Execute(ctx, spanDef(doc.URL, doc.URL, slow.URL), techReq(nWords(10), "de", "Sie LÜGEN euch an."))
	require.Equal(t, job.CodeTimeout, runErr(t, err).Code)
}

func TestTechniqueSpansHideCredentials(t *testing.T) {
	secrets := []string{spanKey, spanTok, spanProxyTok}
	echo := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":{"message":"bad %s %s %s %s"}}`, r.Header.Get("Authorization"), r.Header.Get("X-Proxy-Token"), spanKey, spanTok)
	}
	sentEcho := httptest.NewServer(http.HandlerFunc(echo))
	defer sentEcho.Close()
	llmEcho := newTopicMock(t, echo)
	llmLeak := newTopicMock(t, answer(phrasesReply([2]string{ll, spanKey}, [2]string{ll, "Bearer " + spanTok})))
	doc := docStub(t)
	for name, c := range map[string]struct{ lang, sent, llm string }{
		"sentence echo": {"en", sentEcho.URL, llmEcho.URL},
		"llm echo":      {"de", sentEcho.URL, llmEcho.URL},
		"llm phrases":   {"de", sentEcho.URL, llmLeak.URL},
	} {
		logs := captureLogs(t)
		raw, err := (&Executor{}).Execute(context.Background(), spanDef(doc.URL, c.sent, c.llm), techReq(nWords(10), c.lang, "Sie LÜGEN euch an."))
		require.NoError(t, err, name)
		for _, s := range secrets {
			require.NotContains(t, string(raw), s, name)
			require.NotContains(t, logs.String(), s, name)
		}
	}
	require.Contains(t, techniqueRedactor(spanDef("", "", "").Execution)("k="+spanKey+" "+spanProxyTok+" "+spanTok), "[redacted]")
	require.NotContains(t, techniqueRedactor(spanDef("", "", "").Execution)(spanKey+spanProxyTok+spanTok), "SECRET")
}
