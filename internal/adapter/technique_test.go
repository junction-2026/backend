package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// classifierStub stands in for one ML model service. scoreFn gives the scores of one text (default all 0.1).
type classifierStub struct {
	*httptest.Server
	mu      sync.Mutex
	bodies  [][]string
	hdr     http.Header
	scoreFn func(text string) map[string]float64
	status  int
	raw     string // when set, the exact answer body
}

func newClassifierStub(t *testing.T) *classifierStub {
	s := &classifierStub{status: 200}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Text []string }
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &in)
		s.mu.Lock()
		s.bodies, s.hdr = append(s.bodies, in.Text), r.Header
		s.mu.Unlock()
		w.WriteHeader(s.status)
		if s.raw != "" {
			io.WriteString(w, s.raw)
			return
		}
		lists := []any{}
		for _, txt := range in.Text {
			sc := map[string]float64{}
			if s.scoreFn != nil {
				sc = s.scoreFn(txt)
			}
			l := []any{}
			for _, n := range registry.TechniqueNames {
				v, ok := sc[n]
				if !ok {
					v = 0.1
				}
				l = append(l, map[string]any{"label": n, "score": v})
			}
			lists = append(lists, l)
		}
		json.NewEncoder(w).Encode(map[string]any{"result": lists, "latency_ms": 7.5})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *classifierStub) calls() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.bodies) }

func fp(f float64) *float64 { return &f }

func techDef(short, long string) *registry.Analyzer {
	return &registry.Analyzer{ID: "techniques", Execution: registry.Execution{
		Adapter: registry.AdapterTechniqueClassifier,
		Short:   &registry.TechniqueModel{Endpoint: short, Headers: map[string]string{"Authorization": "Bearer tok-secret"}, Threshold: fp(0.3)},
		Long:    &registry.TechniqueModel{Endpoint: long, Headers: map[string]string{"Authorization": "Bearer tok-secret"}, Threshold: fp(0.5)},
	}}
}

func techReq(text, lang, content string) Request {
	return Request{JobID: "j1", AnalyzerID: "techniques", Inputs: map[string]any{
		"text":        map[string]any{"translated_text": text, "source_language": lang},
		jobContentKey: content,
	}}
}

type techResult struct {
	Model      string             `json:"model"`
	Words      int                `json:"words"`
	Threshold  float64            `json:"threshold"`
	Thresholds map[string]float64 `json:"thresholds"`
	Techniques []struct {
		Name, Display string
		Score         float64
	} `json:"techniques"`
	Detected []struct {
		Name, Display, Explanation string
		Score                      float64
		Spans                      []techniqueSpan
	} `json:"detected"`
	MaxScore  float64 `json:"max_score"`
	Sentences []struct {
		Text       string
		Start, End int
		Techniques []struct {
			Name, Display, Explanation string
			Score                      float64
		}
	} `json:"sentences"`
	OffsetsIn          string   `json:"offsets_in"`
	SentencesTruncated bool     `json:"sentences_truncated"`
	LatencyMS          *float64 `json:"latency_ms"`
	SpanMethod         string   `json:"span_method"`
	SpanStatus         string   `json:"span_status"`
	SpansTruncated     bool     `json:"spans_truncated"`
}

func runTech(t *testing.T, d *registry.Analyzer, r Request) techResult {
	t.Helper()
	raw, err := (&Executor{}).Execute(context.Background(), d, r)
	require.NoError(t, err)
	var res techResult
	require.NoError(t, json.Unmarshal(raw, &res))
	return res
}

func techErr(t *testing.T, d *registry.Analyzer, r Request) *job.RunError {
	t.Helper()
	_, err := (&Executor{}).Execute(context.Background(), d, r)
	return runErr(t, err)
}

func nWords(n int) string { return strings.TrimSpace(strings.Repeat("word ", n)) }

func TestTechniqueRouting(t *testing.T) {
	short, long := newClassifierStub(t), newClassifierStub(t)
	d := techDef(short.URL, long.URL)
	for _, c := range []struct {
		words int
		model string
		thr   float64
	}{{40, "short", 0.3}, {80, "short", 0.3}, {81, "long", 0.5}, {300, "long", 0.5}} {
		s0, l0 := short.calls(), long.calls()
		res := runTech(t, d, techReq(nWords(c.words), "en", ""))
		require.Equal(t, c.model, res.Model, c.words)
		require.Equal(t, c.words, res.Words)
		require.Equal(t, c.thr, res.Threshold)
		require.Equal(t, c.model == "short", short.calls() == s0+1 && long.calls() == l0)
	}
	d.Execution.ShortMaxWords = new(int)
	*d.Execution.ShortMaxWords = 5
	require.Equal(t, "short", runTech(t, d, techReq(nWords(5), "en", "")).Model)
	require.Equal(t, "long", runTech(t, d, techReq(nWords(6), "en", "")).Model)
}

func TestTechniqueIgnoresSource(t *testing.T) {
	s := newClassifierStub(t)
	d := techDef(s.URL, s.URL)
	d.Input.Bindings = map[string]string{"text": "$analyzers.translation"}
	jobWith := func(src job.Source) *job.Job {
		return &job.Job{Input: &job.Input{Content: "They are lying to you now.", Source: src}, Analyzers: []job.AnalyzerRun{
			{ID: "translation", Result: json.RawMessage(`{"translated_text":"They are lying to you now.","source_language":"en"}`)}}}
	}
	run := func(j *job.Job) string {
		raw, err := (&Executor{}).Execute(context.Background(), d, Request{JobID: "j", AnalyzerID: "techniques", Inputs: Inputs(d, j)})
		require.NoError(t, err)
		return string(raw)
	}
	require.Equal(t, run(jobWith(job.Source{})), run(jobWith(job.Source{Platform: "x"})))
}

func TestTechniqueBatchBody(t *testing.T) {
	s := newClassifierStub(t)
	d := techDef(s.URL, s.URL)
	full := "Wake up. They are LYING to you!\nGo!\nWhat do they want?!  Because 3 words ok"
	res := runTech(t, d, techReq(full, "en", ""))
	require.Equal(t, [][]string{{full, "They are LYING to you!", "What do they want?!", "Because 3 words ok"}}, s.bodies)
	require.False(t, res.SentencesTruncated)
	require.Equal(t, "application/json", s.hdr.Get("Content-Type"))
	require.Equal(t, "Bearer tok-secret", s.hdr.Get("Authorization"))

	// a single sentence that is the whole text is sent once
	s.bodies = nil
	runTech(t, d, techReq("One single sentence here.", "en", ""))
	require.Equal(t, [][]string{{"One single sentence here."}}, s.bodies)
	// nothing long enough to split: only the document
	s.bodies = nil
	runTech(t, d, techReq("Too short.", "en", ""))
	require.Equal(t, [][]string{{"Too short."}}, s.bodies)

	// truncation: only the first max_sentences are scored
	s.bodies = nil
	d.Execution.MaxSentences = new(int)
	*d.Execution.MaxSentences = 2
	res = runTech(t, d, techReq("Aaa bbb ccc. Ddd eee fff. Ggg hhh iii.", "en", ""))
	require.Len(t, s.bodies[0], 3)
	require.True(t, res.SentencesTruncated)
	// exactly max_sentences is not truncated
	res = runTech(t, d, techReq("Aaa bbb ccc. Ddd eee fff.", "en", ""))
	require.False(t, res.SentencesTruncated)
}

func TestSplitSentences(t *testing.T) {
	got := splitSentences("  Hi there you... Ok. \r\nFine? Not 😀 so sure!!\n")
	require.Equal(t, []sentence{{"Hi there you...", 2, 17}, {"Not 😀 so sure!!", 30, 46}}, got)
	for _, s := range got { // offsets are UTF-16 units
		u := utf16.Encode([]rune("  Hi there you... Ok. \r\nFine? Not 😀 so sure!!\n"))
		require.Equal(t, s.Text, string(utf16.Decode(u[s.Start:s.End])))
	}
	require.Empty(t, splitSentences(""))
}

func TestTechniqueThresholds(t *testing.T) {
	s := newClassifierStub(t)
	s.scoreFn = func(string) map[string]float64 {
		return map[string]float64{"Loaded_Language": 0.4, "Doubt": 0.6, "Slogans": 0.05}
	}
	d := techDef(s.URL, s.URL)
	text := "They are LYING to you now."
	res := runTech(t, d, techReq(text, "en", text))
	require.Equal(t, "short", res.Model)
	require.Equal(t, []string{"Doubt", "Loaded_Language"}, names(res))

	res = runTech(t, d, techReq(nWords(100)+". "+text, "en", ""))
	require.Equal(t, "long", res.Model)
	require.Equal(t, []string{"Doubt"}, names(res)) // 0.4 < 0.5

	d.Execution.Long.Thresholds = map[string]float64{"Doubt": 0.7, "Slogans": 0}
	res = runTech(t, d, techReq(nWords(100)+". "+text, "en", ""))
	require.Equal(t, []string{"Slogans"}, names(res)) // 0.05 >= 0
	require.Equal(t, 0.7, res.Thresholds["Doubt"])
	require.Equal(t, 0.5, res.Thresholds["Repetition"])
	require.Len(t, res.Thresholds, 18)
	require.Len(t, res.Techniques, 18)
	require.Equal(t, 0.6, res.MaxScore)
	require.Equal(t, "Doubt", res.Techniques[0].Name)
	require.NotNil(t, res.LatencyMS)
	require.Equal(t, 7.5, *res.LatencyMS)
	for _, sd := range res.Sentences {
		for _, x := range sd.Techniques {
			require.GreaterOrEqual(t, x.Score, res.Thresholds[x.Name])
		}
	}
}

func names(r techResult) (out []string) {
	for _, d := range r.Detected {
		out = append(out, d.Name)
	}
	return
}

func TestTechniqueDisplayMapComplete(t *testing.T) {
	require.Len(t, registry.TechniqueNames, 18)
	require.Len(t, techniqueDisplay, 18)
	for _, n := range registry.TechniqueNames {
		i := techniqueDisplay[n]
		require.NotEmpty(t, i.Display, n)
		require.NotEmpty(t, i.Explanation, n)
		require.NotContains(t, i.Explanation, "\n")
	}
	require.Equal(t, techniqueInfo{"Loaded language", "Words chosen to trigger strong emotions"}, techniqueDisplay["Loaded_Language"])

	s := newClassifierStub(t)
	s.scoreFn = func(string) map[string]float64 { return map[string]float64{"Slogans": 0.9} }
	text := "Make them pay now."
	res := runTech(t, techDef(s.URL, s.URL), techReq(text, "en", text))
	for _, x := range res.Techniques {
		require.NotEmpty(t, x.Display)
	}
	require.NotEmpty(t, res.Detected[0].Explanation)
	require.NotEmpty(t, res.Sentences[0].Techniques[0].Display)
	require.NotEmpty(t, res.Sentences[0].Techniques[0].Explanation)
}

// slice16 is s.slice(start, end) of JavaScript.
func slice16(s string, start, end int) string {
	return string(utf16.Decode(utf16.Encode([]rune(s))[start:end]))
}

func TestTechniqueOffsets(t *testing.T) {
	s := newClassifierStub(t)
	s.scoreFn = func(txt string) map[string]float64 {
		if strings.Contains(txt, "LYING") || strings.Contains(txt, "Müll") {
			return map[string]float64{"Doubt": 0.9}
		}
		return nil
	}
	d := techDef(s.URL, s.URL)

	content := "Wake up. They are LYING to you."
	res := runTech(t, d, techReq(content, "en", content))
	require.Equal(t, "original", res.OffsetsIn)
	require.Equal(t, 9, res.Sentences[0].Start)
	require.Equal(t, 31, res.Sentences[0].End)
	require.Equal(t, "They are LYING to you.", res.Sentences[0].Text)

	// non-ASCII and astral characters count in UTF-16 units; the content has extra text before the sentences
	tr := "Ünï ok now. 😀 They are LYING to you ✓."
	c := "😀😀 Ünï ok now. 😀 They are LYING to you ✓."
	res = runTech(t, d, techReq(tr, "en", c))
	require.Equal(t, "original", res.OffsetsIn)
	require.Len(t, res.Sentences, 1)
	require.Equal(t, 17, res.Sentences[0].Start)
	require.Equal(t, 44, res.Sentences[0].End)
	require.Equal(t, res.Sentences[0].Text, slice16(c, 17, 44))

	// English, but a reported sentence is not verbatim in the content: offsets index the translation
	res = runTech(t, d, techReq(tr, "en", "<p>Ünï ok now. 😀 They are lying to you ✓.</p>"))
	require.Equal(t, "translation", res.OffsetsIn)
	require.Equal(t, res.Sentences[0].Text, slice16(tr, res.Sentences[0].Start, res.Sentences[0].End))

	// German: always the translation, even when the content happens to contain the sentence
	de := "Das ist 😀 Müll heute. Wirklich Müll hier."
	res = runTech(t, d, techReq(de, "de", de))
	require.Equal(t, "translation", res.OffsetsIn)
	require.Len(t, res.Sentences, 2)
	require.Equal(t, 0, res.Sentences[0].Start)
	require.Equal(t, 23, res.Sentences[1].Start)
	for _, sd := range res.Sentences {
		require.Equal(t, sd.Text, slice16(de, sd.Start, sd.End))
	}

	// English, sentences present but in another order: not "in order", so the translation is indexed
	en := "They are LYING now. Das ist Müll heute."
	res = runTech(t, d, techReq(en, "en", "Das ist Müll heute. They are LYING now."))
	require.Equal(t, "translation", res.OffsetsIn)
	// no sentence reported: still the translation
	res = runTech(t, d, techReq("Nothing here at all.", "en", "Nothing here at all."))
	require.Equal(t, "original", res.OffsetsIn) // vacuously every reported sentence was found
	require.Empty(t, res.Sentences)
}

func TestTechniqueInvalidOutput(t *testing.T) {
	label := func(n string, score any) map[string]any { return map[string]any{"label": n, "score": score} }
	full := func(mut func([]any) []any) string {
		l := []any{}
		for _, n := range registry.TechniqueNames {
			l = append(l, label(n, 0.2))
		}
		b, _ := json.Marshal(map[string]any{"result": []any{mut(l)}})
		return string(b)
	}
	unnamed := []any{}
	for i := range 18 {
		unnamed = append(unnamed, label(fmt.Sprintf("LABEL_%d", i), 0.2))
	}
	ub, _ := json.Marshal(map[string]any{"result": []any{unnamed}})
	cases := map[string]string{
		"LABEL_n":         string(ub),
		"17 labels":       full(func(l []any) []any { return l[:17] }),
		"19 labels":       full(func(l []any) []any { return append(l, label("Doubt", 0.1)) }),
		"unknown label":   full(func(l []any) []any { l[0] = label("Nonsense", 0.1); return l }),
		"duplicate label": full(func(l []any) []any { l[1] = label(registry.TechniqueNames[0], 0.1); return l }),
		"missing label":   full(func(l []any) []any { l[0] = map[string]any{"score": 0.1}; return l }),
		"score 1.2":       full(func(l []any) []any { l[0] = label(registry.TechniqueNames[0], 1.2); return l }),
		"score -0.1":      full(func(l []any) []any { l[0] = label(registry.TechniqueNames[0], -0.1); return l }),
		"score string":    full(func(l []any) []any { l[0] = label(registry.TechniqueNames[0], "0.5"); return l }),
		"missing score":   full(func(l []any) []any { l[0] = map[string]any{"label": registry.TechniqueNames[0]}; return l }),
		"two lists":       strings.Replace(full(func(l []any) []any { return l }), `"result":[`, `"result":[[],`, 1),
		"not an object":   `[1,2]`,
		"not json":        `<html>`,
		"missing result":  `{"latency_ms":1}`,
		"result object":   `{"result":{}}`,
		"result null":     `{"result":null}`,
		"bad latency":     strings.Replace(full(func(l []any) []any { return l }), `}]]}`, `}]],"latency_ms":"fast"}`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s := newClassifierStub(t)
			s.raw = body
			e := techErr(t, techDef(s.URL, s.URL), techReq("They are LYING to you now.", "en", ""))
			require.Equal(t, job.CodeInvalidOutput, e.Code, e.Message)
		})
	}
	// the same body but for a one-item batch is valid
	s := newClassifierStub(t)
	s.raw = full(func(l []any) []any { return l })
	runTech(t, techDef(s.URL, s.URL), techReq("They are LYING to you now.", "en", ""))
}

func TestTechniqueServiceFailuresNoFallback(t *testing.T) {
	short, long := newClassifierStub(t), newClassifierStub(t)
	short.status = 503
	e := techErr(t, techDef(short.URL, long.URL), techReq(nWords(10), "en", ""))
	require.Equal(t, job.CodeAdapterError, e.Code)
	require.Equal(t, 1, short.calls()) // no retry
	require.Zero(t, long.calls())

	long.status = 500
	e = techErr(t, techDef(short.URL, long.URL), techReq(nWords(200), "en", ""))
	require.Equal(t, job.CodeAdapterError, e.Code)
	require.Equal(t, 1, long.calls())
	require.Equal(t, 1, short.calls())

	// connection error
	dead := newClassifierStub(t)
	url := dead.URL
	dead.Close()
	e = techErr(t, techDef(url, url), techReq(nWords(10), "en", ""))
	require.Equal(t, job.CodeAdapterError, e.Code)
	require.NotContains(t, e.Message, url)
}

func TestTechniqueTimeoutAndCancel(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer slow.Close()
	d := techDef(slow.URL, slow.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := (&Executor{}).Execute(ctx, d, techReq(nWords(10), "en", ""))
	require.Equal(t, job.CodeTimeout, runErr(t, err).Code)

	ctx, cancel2 := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel2)
	_, err = (&Executor{}).Execute(ctx, d, techReq(nWords(10), "en", ""))
	require.Equal(t, job.CodeCancelled, runErr(t, err).Code)
}

func TestTechniqueUnreadableTranslation(t *testing.T) {
	s := newClassifierStub(t)
	d := techDef(s.URL, s.URL)
	for name, in := range map[string]any{
		"nil":         nil,
		"no text":     map[string]any{"source_language": "en"},
		"number":      map[string]any{"translated_text": 5},
		"null":        map[string]any{"translated_text": nil},
		"blank":       map[string]any{"translated_text": "  "},
		"not object":  "text",
		"missing key": "<absent>",
	} {
		r := Request{JobID: "j", AnalyzerID: "techniques", Inputs: map[string]any{"text": in}}
		if name == "missing key" {
			r.Inputs = map[string]any{}
		}
		require.Equal(t, job.CodeAdapterError, techErr(t, d, r).Code, name)
	}
	require.Zero(t, s.calls())
}

func TestTechniqueHidesCredentials(t *testing.T) {
	const tok = "tok-secret"
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":"bad %s","auth":"%s"}`, r.Header.Get("Authorization"), tok)
	}))
	defer echo.Close()
	e := techErr(t, techDef(echo.URL, echo.URL), techReq(nWords(10), "en", ""))
	require.Equal(t, job.CodeAdapterError, e.Code)
	require.NotContains(t, e.Message, tok)

	// an unknown label that carries the token must not reach the message
	s := newClassifierStub(t)
	s.raw = fmt.Sprintf(`{"result":[[{"label":"%s","score":0.1}]]}`, "Bearer "+tok)
	e = techErr(t, techDef(s.URL, s.URL), techReq(nWords(10), "en", ""))
	require.Equal(t, job.CodeInvalidOutput, e.Code)
	require.NotContains(t, e.Message, tok)
	require.NotContains(t, techniqueRedactor(techDef("", "").Execution)("x "+tok+" Bearer "+tok), tok)
}
