package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
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
	wait    func() // when set, runs after the request is recorded and before answering
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
		if s.wait != nil {
			s.wait()
		}
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

const tokTech, tokSocial = "tok-secret-tech", "tok-secret-social"

func techDef(technique, social string) *registry.Analyzer {
	return &registry.Analyzer{ID: "techniques", Execution: registry.Execution{
		Adapter:   registry.AdapterTechniqueClassifier,
		Technique: &registry.NCIModel{Endpoint: technique, Headers: map[string]string{"Authorization": "Bearer " + tokTech}},
		Social:    &registry.NCIModel{Endpoint: social, Headers: map[string]string{"Authorization": "Bearer " + tokSocial}},
	}}
}

func techReq(text, lang, content string) Request {
	return Request{JobID: "j1", AnalyzerID: "techniques", Inputs: map[string]any{
		"text":        map[string]any{"translated_text": text, "source_language": lang},
		jobContentKey: content,
	}}
}

type techResult struct {
	Words    int           `json:"words"`
	Results  []modelResult `json:"results"`
	Detected []struct {
		Name, Display, Explanation string
		Models                     []string
		Spans                      []techniqueSpan
		Margin                     *float64
		Located                    *bool
		Strength                   string
	} `json:"detected"`
	Summary        map[string]int `json:"summary"`
	SpanMethod     string         `json:"span_method"`
	SpanStatus     string         `json:"span_status"`
	SpansTruncated bool           `json:"spans_truncated"`
}

func (r techResult) models() (out []string) {
	for _, m := range r.Results {
		out = append(out, m.Model)
	}
	return
}

// score is the TechniqueScore of label in the ModelResult of model.
func (r techResult) score(t *testing.T, model, label string) techniqueScore {
	t.Helper()
	for _, m := range r.Results {
		for _, s := range m.Scores {
			if m.Model == model && s.Label == label {
				return s
			}
		}
	}
	t.Fatalf("no %s score from %s", label, model)
	return techniqueScore{}
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
	tech, social := newClassifierStub(t), newClassifierStub(t)
	d := techDef(tech.URL, social.URL)
	check := func(words int, want ...string) {
		t.Helper()
		t0, s0 := tech.calls(), social.calls()
		text := nWords(words)
		res := runTech(t, d, techReq(text, "en", ""))
		require.Equal(t, want, res.models(), words)
		require.Equal(t, words, res.Words)
		usesT, usesS := slices.Contains(want, "nci-technique"), slices.Contains(want, "nci-social")
		require.Equal(t, usesT, tech.calls() == t0+1, words)
		require.Equal(t, !usesT, tech.calls() == t0, words) // the unselected model receives no request
		require.Equal(t, usesS, social.calls() == s0+1, words)
		require.Equal(t, !usesS, social.calls() == s0, words)
		if usesT {
			require.Equal(t, []string{text}, tech.bodies[len(tech.bodies)-1]) // one text, no sentence batch
		}
		if usesS {
			require.Equal(t, []string{text}, social.bodies[len(social.bodies)-1])
		}
	}
	check(1, "nci-social")
	check(39, "nci-social")
	check(40, "nci-technique", "nci-social")
	check(80, "nci-technique", "nci-social")
	check(81, "nci-technique")
	check(300, "nci-technique")
	require.Equal(t, "Bearer "+tokTech, tech.hdr.Get("Authorization"))
	require.Equal(t, "Bearer "+tokSocial, social.hdr.Get("Authorization"))
	require.Equal(t, "application/json", tech.hdr.Get("Content-Type"))

	d.Execution.SocialMaxWords, d.Execution.TechniqueMinWords = ip(5), ip(3)
	check(2, "nci-social")
	check(3, "nci-technique", "nci-social")
	check(5, "nci-technique", "nci-social")
	check(6, "nci-technique")
	d.Execution.TechniqueMinWords = ip(6) // social_max_words + 1: no overlap, no gap
	check(5, "nci-social")
	check(6, "nci-technique")
}

func ip(n int) *int { return &n }

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

func TestSplitSentences(t *testing.T) {
	got := splitSentences("  Hi there you... Ok. \r\nFine? Not 😀 so sure!!\n")
	require.Equal(t, []sentence{{"Hi there you...", 2, 17}, {"Not 😀 so sure!!", 30, 46}}, got)
	for _, s := range got { // offsets are UTF-16 units
		u := utf16.Encode([]rune("  Hi there you... Ok. \r\nFine? Not 😀 so sure!!\n"))
		require.Equal(t, s.Text, string(utf16.Decode(u[s.Start:s.End])))
	}
	require.Empty(t, splitSentences(""))
}

func TestTechniqueCutoffs(t *testing.T) {
	tech, social := newClassifierStub(t), newClassifierStub(t)
	tech.scoreFn = func(string) map[string]float64 {
		return map[string]float64{"Loaded_Language": 0.4, "Doubt": 0.49, "Slogans": 0.5, "Bandwagon": 0.9}
	}
	social.scoreFn = func(string) map[string]float64 {
		return map[string]float64{"Loaded_Language": 0.4, "Doubt": 0.3, "Slogans": 0.29, "Bandwagon": 0.9}
	}
	d := techDef(tech.URL, social.URL)
	res := runTech(t, d, techReq(nWords(50), "en", ""))
	require.Equal(t, []string{"nci-technique", "nci-social"}, res.models())
	require.Equal(t, 0.5, res.Results[0].Cutoff) // defaults
	require.Equal(t, 0.3, res.Results[1].Cutoff)
	for _, m := range res.Results {
		require.Len(t, m.Scores, 18) // below-cutoff scores are present
		require.Equal(t, 7.5, *m.LatencyMS)
		for i, n := range registry.TechniqueNames {
			s := m.Scores[i]
			require.Equal(t, n, s.Label)
			require.Equal(t, techniqueDisplay[n].Display, s.Display)
			require.Equal(t, m.Model, s.Model)
			require.Equal(t, m.Cutoff, s.Cutoff)
			require.True(t, s.Known)
			require.Equal(t, s.Score >= s.Cutoff, s.AboveCutoff)
		}
	}
	require.Equal(t, techniqueScore{"Doubt", "Doubt", 0.49, "nci-technique", 0.5, false, true}, res.score(t, "nci-technique", "Doubt"))
	require.True(t, res.score(t, "nci-technique", "Slogans").AboveCutoff) // equality counts
	require.True(t, res.score(t, "nci-social", "Doubt").AboveCutoff)
	require.False(t, res.score(t, "nci-social", "Slogans").AboveCutoff)
	require.False(t, res.score(t, "nci-technique", "Loaded_Language").AboveCutoff) // same score, different cutoffs
	require.True(t, res.score(t, "nci-social", "Loaded_Language").AboveCutoff)

	// detected: known labels above cutoff in any model, names order, with the models that flagged them
	require.Equal(t, []string{"Loaded_Language", "Doubt", "Bandwagon", "Slogans"}, names(res))
	models := map[string][]string{}
	for _, x := range res.Detected {
		models[x.Name] = x.Models
		require.NotEmpty(t, x.Display)
		require.NotEmpty(t, x.Explanation)
		require.Equal(t, []techniqueSpan{}, x.Spans)
	}
	require.Equal(t, map[string][]string{"Loaded_Language": {"nci-social"}, "Doubt": {"nci-social"},
		"Bandwagon": {"nci-technique", "nci-social"}, "Slogans": {"nci-technique"}}, models)

	// configured cutoffs are echoed and used
	d.Execution.Technique.Cutoff, d.Execution.Social.Cutoff = fp(0.6), fp(0.95)
	res = runTech(t, d, techReq(nWords(50), "en", ""))
	require.Equal(t, 0.6, res.Results[0].Cutoff)
	require.Equal(t, 0.6, res.score(t, "nci-technique", "Repetition").Cutoff)
	require.False(t, res.score(t, "nci-technique", "Slogans").AboveCutoff)
	require.Equal(t, []string{"Bandwagon"}, names(res))
	require.Equal(t, []string{"nci-technique"}, res.Detected[0].Models)
}

func TestTechniqueNoAveraging(t *testing.T) {
	tech, social := newClassifierStub(t), newClassifierStub(t)
	tech.scoreFn = func(string) map[string]float64 { return map[string]float64{"Doubt": 0.6} }
	social.scoreFn = func(string) map[string]float64 { return map[string]float64{"Doubt": 0.2} }
	raw, err := (&Executor{}).Execute(context.Background(), techDef(tech.URL, social.URL), techReq(nWords(60), "en", ""))
	require.NoError(t, err)
	var res techResult
	require.NoError(t, json.Unmarshal(raw, &res))
	require.Equal(t, 0.6, res.score(t, "nci-technique", "Doubt").Score)
	require.Equal(t, 0.2, res.score(t, "nci-social", "Doubt").Score)
	require.Equal(t, 2, strings.Count(string(raw), `"label":"Doubt"`))
	require.Equal(t, []string{"nci-technique"}, res.Detected[0].Models)

	// the result has only the shared members: no top-level score summary, no Detection score
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &top))
	require.ElementsMatch(t, []string{"words", "results", "detected", "span_method", "span_status", "spans_truncated",
		"explanation_language", "glossary_version", "summary"}, slices.Collect(maps.Keys(top)))
	var det []map[string]any
	require.NoError(t, json.Unmarshal(top["detected"], &det))
	require.ElementsMatch(t, []string{"name", "display", "explanation", "models", "spans", "plain_language",
		"margin", "located", "strength"}, slices.Collect(maps.Keys(det[0])))
}

func TestTechniqueNothingDetected(t *testing.T) {
	s := newClassifierStub(t)
	res := runTech(t, techDef(s.URL, s.URL), techReq(nWords(100), "en", ""))
	require.Equal(t, []string{"nci-technique"}, res.models())
	require.NotNil(t, res.Detected)
	require.Empty(t, res.Detected)
	require.Len(t, res.Results[0].Scores, 18)
	for _, sc := range res.Results[0].Scores {
		require.Equal(t, 0.1, sc.Score)
		require.False(t, sc.AboveCutoff)
	}
}

func TestTechniqueUnfamiliarLabel(t *testing.T) {
	l := []any{map[string]any{"label": "Appeal_to_Pity", "score": 0.7}}
	for _, n := range registry.TechniqueNames {
		l = append(l, map[string]any{"label": n, "score": 0.1})
	}
	l = append(l, map[string]any{"label": "<b>LIES</b>", "score": 0.1})
	b, _ := json.Marshal(map[string]any{"result": []any{l}})
	s := newClassifierStub(t)
	s.raw = string(b)
	res := runTech(t, techDef(s.URL, s.URL), techReq(nWords(10), "en", ""))
	sc := res.Results[0].Scores
	require.Len(t, sc, 20)
	require.Equal(t, "Loaded_Language", sc[0].Label)
	require.Equal(t, techniqueScore{"Appeal_to_Pity", "Appeal_to_Pity", 0.7, "nci-social", 0.3, true, false}, sc[18])
	require.Equal(t, "<b>LIES</b>", sc[19].Label)
	require.False(t, sc[19].Known)
	require.Empty(t, res.Detected) // unfamiliar labels never reach detected
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

}

// slice16 is s.slice(start, end) of JavaScript.
func slice16(s string, start, end int) string {
	return string(utf16.Decode(utf16.Encode([]rune(s))[start:end]))
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
		"known replaced":  full(func(l []any) []any { l[0] = label("Nonsense", 0.1); return l }),
		"empty label":     full(func(l []any) []any { return append(l, label("", 0.1)) }),
		"null label":      full(func(l []any) []any { return append(l, label("x", 0.1), map[string]any{"label": nil, "score": 0.1}) }),
		"number label":    full(func(l []any) []any { return append(l, label("x", 0.1), map[string]any{"label": 3, "score": 0.1}) }),
		"extra dup":       full(func(l []any) []any { return append(l, label("x", 0.1), label("x", 0.2)) }),
		"extra score 2":   full(func(l []any) []any { return append(l, label("x", 2)) }),
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
			for _, words := range []int{10, 100} { // nci-social, nci-technique
				s := newClassifierStub(t)
				s.raw = body
				e := techErr(t, techDef(s.URL, s.URL), techReq(nWords(words), "en", ""))
				require.Equal(t, job.CodeInvalidOutput, e.Code, e.Message)
			}
		})
	}
	// the full list, with or without an extra label, is valid
	s := newClassifierStub(t)
	s.raw = full(func(l []any) []any { return l })
	runTech(t, techDef(s.URL, s.URL), techReq("They are LYING to you now.", "en", ""))
	s.raw = full(func(l []any) []any { return append(l, label("Appeal_to_Pity", 0)) })
	runTech(t, techDef(s.URL, s.URL), techReq("They are LYING to you now.", "en", ""))
}

func TestTechniqueServiceFailuresNoFallback(t *testing.T) {
	tech, social := newClassifierStub(t), newClassifierStub(t)
	social.status = 503
	e := techErr(t, techDef(tech.URL, social.URL), techReq(nWords(10), "en", ""))
	require.Equal(t, job.CodeAdapterError, e.Code)
	require.Equal(t, 1, social.calls()) // no retry
	require.Zero(t, tech.calls())       // no fallback

	social.status, tech.status = 200, 503
	e = techErr(t, techDef(tech.URL, social.URL), techReq(nWords(200), "en", ""))
	require.Equal(t, job.CodeAdapterError, e.Code)
	require.Equal(t, 1, tech.calls())
	require.Equal(t, 1, social.calls())

	// connection error
	dead := newClassifierStub(t)
	url := dead.URL
	dead.Close()
	e = techErr(t, techDef(url, url), techReq(nWords(10), "en", ""))
	require.Equal(t, job.CodeAdapterError, e.Code)
	require.NotContains(t, e.Message, url)
}

func TestTechniquePartialFailure(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer slow.Close()
	for name, fail := range map[string]string{"nci-technique": "technique", "nci-social": "social"} {
		t.Run(name, func(t *testing.T) {
			ok, bad := newClassifierStub(t), newClassifierStub(t)
			bad.status = 503
			// The failure cancels the other request, so answer only once the healthy model has received its
			// request: both calls then overlap, which is what the call counts below check.
			bad.wait = func() {
				for deadline := time.Now().Add(2 * time.Second); ok.calls() == 0 && time.Now().Before(deadline); {
					time.Sleep(time.Millisecond)
				}
			}
			d := techDef(ok.URL, bad.URL)
			if fail == "technique" {
				d = techDef(bad.URL, ok.URL)
			}
			raw, err := (&Executor{}).Execute(context.Background(), d, techReq(nWords(60), "en", ""))
			require.Nil(t, raw)
			e := runErr(t, err)
			require.Equal(t, job.CodeAdapterError, e.Code)
			require.Contains(t, e.Message, name)
			require.Equal(t, 1, ok.calls()) // both were called concurrently
			require.Equal(t, 1, bad.calls())

			// the failure does not wait for the other, slow model
			bad.wait = nil
			d = techDef(slow.URL, bad.URL)
			if fail == "technique" {
				d = techDef(bad.URL, slow.URL)
			}
			start := time.Now()
			e = techErr(t, d, techReq(nWords(60), "en", ""))
			require.Equal(t, job.CodeAdapterError, e.Code)
			require.Less(t, time.Since(start), 2*time.Second)
		})
	}
}

func TestTechniqueNonEnglishJobSendsOnlyTranslation(t *testing.T) {
	const german = "Sie LÜGEN euch an und die Regierung verschweigt die Wahrheit über alles was heute passiert"
	tech, social := newClassifierStub(t), newClassifierStub(t)
	d := techDef(tech.URL, social.URL)
	d.Input.Bindings = map[string]string{"text": "$analyzers.translation"}
	english := nWords(50)
	tr, _ := json.Marshal(map[string]any{"translated_text": english, "source_language": "de"})
	j := &job.Job{Input: &job.Input{Content: german}, Analyzers: []job.AnalyzerRun{{ID: "translation", Result: tr}}}
	_, err := (&Executor{}).Execute(context.Background(), d, Request{JobID: "j", AnalyzerID: "techniques", Inputs: Inputs(d, j)})
	require.NoError(t, err)
	require.Equal(t, [][]string{{english}}, tech.bodies)
	require.Equal(t, [][]string{{english}}, social.bodies)
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
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":"bad %s","auth":"%s %s"}`, r.Header.Get("Authorization"), tokTech, tokSocial)
	}))
	defer echo.Close()
	// a label carrying the token in a list that misses known labels
	leak := newClassifierStub(t)
	leak.raw = fmt.Sprintf(`{"result":[[{"label":"Bearer %s %s","score":0.1}]]}`, tokTech, tokSocial)
	ok := newClassifierStub(t)
	for _, bad := range []string{echo.URL, leak.URL} {
		for words, d := range map[int]*registry.Analyzer{ // the failing model is selected: alone or with the other
			100: techDef(bad, ok.URL), 60: techDef(bad, ok.URL), 59: techDef(ok.URL, bad), 10: techDef(ok.URL, bad),
		} {
			e := techErr(t, d, techReq(nWords(words), "en", ""))
			require.Contains(t, []string{job.CodeAdapterError, job.CodeInvalidOutput}, e.Code)
			require.NotContains(t, e.Message, "tok-secret")
		}
	}
	r := techniqueRedactor(techDef("", "").Execution)("x " + tokTech + " Bearer " + tokSocial)
	require.NotContains(t, r, "tok-secret")
}
