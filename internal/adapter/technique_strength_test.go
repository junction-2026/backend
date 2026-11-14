package adapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

)

type strengthOfDet struct {
	Margin   float64
	Located  bool
	Strength string
	Spans    int
}

func strengths(r techResult) map[string]strengthOfDet {
	out := map[string]strengthOfDet{}
	for _, d := range r.Detected {
		out[d.Name] = strengthOfDet{*d.Margin, *d.Located, d.Strength, len(d.Spans)}
	}
	return out
}

func scoreStub(t *testing.T, sc map[string]float64) *classifierStub {
	s := newClassifierStub(t)
	s.scoreFn = func(string) map[string]float64 { return sc }
	return s
}

// Parent criteria 1 and 3: margin per Detection, rounding, highest across models, equality is strong, summary.
func TestTechniqueStrength(t *testing.T) {
	tech := scoreStub(t, map[string]float64{"Loaded_Language": 0.70, "Slogans": 0.65, "Doubt": 0.55})
	social := scoreStub(t, map[string]float64{"Bandwagon": 0.44, "Doubt": 0.5})
	d := techDef(tech.URL, social.URL)
	d.Execution.Social.Cutoff = fp(0.4)
	raw, err := (&Executor{}).Execute(context.Background(), d, techReq(nWords(50), "en", "")) // both models
	require.NoError(t, err)
	var r techResult
	require.NoError(t, json.Unmarshal(raw, &r))
	require.Equal(t, map[string]strengthOfDet{
		"Loaded_Language": {0.2, false, "strong", 0},
		"Slogans":         {0.15, false, "strong", 0}, // 0.65-0.5 = 0.15000000000000002 rounds to 0.15
		"Doubt":           {0.1, false, "weak", 0},    // max(0.55-0.5, 0.5-0.4)
		"Bandwagon":       {0.04, false, "weak", 0},
	}, strengths(r))
	require.Equal(t, map[string]int{"strong": 2, "weak": 2}, r.Summary)
	require.Contains(t, string(raw), `"margin":0.04,`) // serialized rounded, not 0.04000000000000001

	// Parent example: Bandwagon 0.44 (cutoff 0.4) and Loaded_Language 0.70 (cutoff 0.5) only.
	tech.scoreFn = func(string) map[string]float64 { return map[string]float64{"Loaded_Language": 0.70} }
	social.scoreFn = func(string) map[string]float64 { return map[string]float64{"Bandwagon": 0.44} }
	r = runTech(t, d, techReq(nWords(50), "en", ""))
	require.Equal(t, map[string]int{"strong": 1, "weak": 1}, r.Summary)
	require.Equal(t, strengthOfDet{0.04, false, "weak", 0}, strengths(r)["Bandwagon"])

	// Highest margin across models: 0.55 (cutoff 0.5) and 0.5 (cutoff 0.3) -> 0.2.
	d.Execution.Social.Cutoff = nil
	tech.scoreFn = func(string) map[string]float64 { return map[string]float64{"Doubt": 0.55} }
	social.scoreFn = func(string) map[string]float64 { return map[string]float64{"Doubt": 0.5} }
	r = runTech(t, d, techReq(nWords(50), "en", ""))
	require.Equal(t, strengthOfDet{0.2, false, "strong", 0}, strengths(r)["Doubt"])

	// Configured strong_margin.
	d.Execution.Social.Cutoff = fp(0.4)
	d.Execution.StrongMargin = 0.05
	tech.scoreFn = func(string) map[string]float64 { return nil }
	social.scoreFn = func(string) map[string]float64 { return map[string]float64{"Bandwagon": 0.46} }
	r = runTech(t, d, techReq(nWords(10), "en", ""))
	require.Equal(t, strengthOfDet{0.06, false, "strong", 0}, strengths(r)["Bandwagon"])
}

func TestTechniqueStrengthNothingDetected(t *testing.T) {
	s := newClassifierStub(t)
	r := runTech(t, techDef(s.URL, s.URL), techReq("calm words", "en", ""))
	require.Empty(t, r.Detected)
	require.Equal(t, map[string]int{"strong": 0, "weak": 0}, r.Summary)
}

// Parent criterion 2: a located Detection is strong whatever its margin; a failed span step leaves margin alone.
func TestTechniqueStrengthLocated(t *testing.T) {
	const content = "They are LYING to you. Nice weather today."
	doc := scoreStub(t, map[string]float64{"Doubt": 0.31}) // social cutoff 0.3: margin 0.01
	sent := scoreStub(t, map[string]float64{"Doubt": 0.9})
	llm := newTopicMock(t, answer(phrasesReply()))
	r := runTech(t, spanDef(doc.URL, sent.URL, llm.URL), techReq(content, "en", content))
	require.Equal(t, "ok", r.SpanStatus)
	require.Equal(t, strengthOfDet{0.01, true, "strong", 2}, strengths(r)["Doubt"])

	sent.status = 503
	r = runTech(t, spanDef(doc.URL, sent.URL, llm.URL), techReq(content, "en", content))
	require.Equal(t, "failed", r.SpanStatus)
	require.Equal(t, strengthOfDet{0.01, false, "weak", 0}, strengths(r)["Doubt"])
	require.Equal(t, map[string]int{"strong": 0, "weak": 1}, r.Summary)
}

