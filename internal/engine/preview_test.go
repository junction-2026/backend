package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
)

// pv builds the preview of id's result and returns it as comparable JSON-decoded data.
func pv(t *testing.T, id, result string) any {
	t.Helper()
	p := buildPreview(id, raw(result))
	if p == nil {
		return nil
	}
	b, err := json.Marshal(p)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

func j(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		panic(err)
	}
	return v
}

func TestPreviewSimpleShapes(t *testing.T) {
	for name, c := range map[string]struct{ id, result, want string }{
		"translation translated": {"translation", `{"translated_text":"x","source_language":"de","model":"m","usage":{"a":1}}`,
			`{"source_language":"de","translated":true}`},
		"translation english":    {"translation", `{"source_language":"en","skipped":"detected_english"}`, `{"source_language":"en","translated":false}`},
		"translation skipped":    {"translation", `{"source_language":"de","skipped":"x"}`, `{"source_language":"de","translated":false}`},
		"translation wrong type": {"translation", `{"source_language":5,"skipped":7}`, `{"source_language":null,"translated":true}`},
		"propaganda":             {"propaganda_detection", `{"label":"propaganda","score":0.93,"latency_ms":3}`, `{"label":"propaganda","score":0.93}`},
		"propaganda wrong":       {"propaganda_detection", `{"label":1,"score":"x"}`, `{"label":null,"score":null}`},
		"topics capped": {"topic_extraction", `{"claims":[{"text":"c1"},{"text":"c2"},{"text":"c3"},{"text":"c4"},{"text":"c5"},{"text":"c6"},{"text":"c7"}],
			"topics":[{"title":"t1","query":"q"},{"title":"t2"}],"usage":{"total_tokens":9}}`,
			`{"claims":["c1","c2","c3","c4","c5"],"topics":["t1","t2"],"claims_total":7,"topics_total":2}`},
		"topics wrong":     {"topic_extraction", `{"claims":"x","topics":[{"title":3},{"title":"ok"}]}`, `{"claims":[],"topics":["ok"],"claims_total":0,"topics_total":2}`},
		"topics empty":     {"topic_extraction", `{}`, `{"claims":[],"topics":[],"claims_total":0,"topics_total":0}`},
		"news outlets cap": {"news_search", newsResult(12, 8), `{"articles":12,"outlets":["d0","d1","d2","d3","d4","d5"],"languages":["de","en"]}`},
		"news wrong":       {"news_search", `{"articles":"x","languages":"de"}`, `{"articles":null,"outlets":[],"languages":[]}`},
		"fact checks": {"fact_check", `{"fact_checks":[{"publisher":"P1","rating":"False","rating_normalized":"false","url":"u1","claim":"c"},{"publisher":"P2","rating":null,"rating_normalized":"unknown","url":"u2"},
			{"publisher":"P3","rating":"x","rating_normalized":"true","url":"u3"},{"publisher":"P4","url":"u4"}],"requests_used":3}`,
			`{"fact_checks":4,"top":[{"publisher":"P1","rating":"False","rating_normalized":"false","url":"u1"},{"publisher":"P2","rating":null,"rating_normalized":"unknown","url":"u2"},{"publisher":"P3","rating":"x","rating_normalized":"true","url":"u3"}]}`},
		"fact checks wrong": {"fact_check", `{"fact_checks":"x"}`, `{"fact_checks":null,"top":[]}`},
		"fact checks junk":  {"fact_check", `{"fact_checks":[1,{"publisher":"P"}]}`, `{"fact_checks":2,"top":[{"publisher":"P","rating":null,"rating_normalized":null,"url":null}]}`},
		"source info": {"source_info", `{"articles":[{"url":"x"}],"summary":{"articles":1,"matched":1,"bias":{"center":1},"countries":{"DE":1},"outlets":1},"page_outlet":{"domain":"spiegel.de","name":"Spiegel"}}`,
			`{"summary":{"articles":1,"matched":1,"bias":{"center":1},"countries":{"DE":1},"outlets":1},"page_outlet":{"domain":"spiegel.de","name":"Spiegel"}}`},
		"source info wrong": {"source_info", `{"summary":3,"page_outlet":null}`, `{"summary":null,"page_outlet":null}`},
	} {
		t.Run(name, func(t *testing.T) { require.Equal(t, j(c.want), pv(t, c.id, c.result)) })
	}
}

func newsResult(articles, domains int) string {
	var as []string
	for i := 0; i < articles; i++ {
		as = append(as, fmt.Sprintf(`{"url":"u%d","domain":"d%d"}`, i, i%domains))
	}
	return `{"languages":["de","en"],"articles":[` + strings.Join(as, ",") + `]}`
}

func TestPreviewNoShapeAndBadResults(t *testing.T) {
	require.Nil(t, buildPreview("text_stats", raw(`{"words":3}`)))
	require.Nil(t, buildPreview("unknown", raw(`{}`)))
	for _, id := range []string{"translation", "propaganda_detection", "topic_extraction", "techniques", "llm_techniques", "news_search", "fact_check", "source_info"} {
		require.NotNil(t, buildPreview(id, raw(`{}`)), id)
		for _, bad := range []string{``, `null`, `[1]`, `"x"`, `{`} {
			require.Nil(t, buildPreview(id, raw(bad)), "%s %q", id, bad)
		}
	}
}

func utf16Slice(s string, start, end int) string {
	return string(utf16.Decode(utf16.Encode([]rune(s))[start:end]))
}

func u16(s, sub string) (int, int) {
	i := strings.Index(s, sub)
	start := len(utf16.Encode([]rune(s[:i])))
	return start, start + len(utf16.Encode([]rune(sub)))
}

func TestTechniquesPreviewGerman(t *testing.T) {
	content := "Äußerst 😀 SCHRECKLICH! Alle lügen, immer."
	s1, e1 := u16(content, "SCHRECKLICH")
	s2, e2 := u16(content, "lügen")
	det := func(name string, spans string) string {
		return fmt.Sprintf(`{"name":%q,"display":"D","explanation":"E","models":["nci-social"],"spans":[%s],
			"plain_language":{"name":"%s-de","definition":"Def %s","why":"Warum %s"}}`, name, spans, name, name, name)
	}
	span := func(s, e int, text string) string {
		return fmt.Sprintf(`{"technique":"x","text":%q,"start":%d,"end":%d,"why":"Weil %s"}`, text, s, e, text)
	}
	var detected []string
	scores := []string{}
	for i := 0; i < 10; i++ { // 10 detections; Loaded_Language_i has score 0.5+i/100, T9 is below the cutoff
		name := fmt.Sprintf("T%d", i)
		spans := span(s1, e1, "SCHRECKLICH") + "," + span(s2, e2, "lügen") + "," + span(s1, e1, "SCHRECKLICH") + "," + span(s2, e2, "lügen")
		detected = append(detected, det(name, spans))
		scores = append(scores, fmt.Sprintf(`{"label":%q,"score":%v,"above_cutoff":%v}`, name, 0.5+float64(i)/100, i != 9))
	}
	detected = append(detected, det("NoScore", ""))
	res := `{"words":9,"results":[{"model":"nci-social","scores":[` + strings.Join(scores, ",") + `]},{"model":"nci-technique","scores":[{"label":"T0","score":0.95,"above_cutoff":true},{"label":"T1","score":0.99,"above_cutoff":false}]}],
		"detected":[` + strings.Join(detected, ",") + `],"explanation_language":"de","glossary_version":"2026-11-1","span_method":"x"}`
	p := pv(t, "techniques", res).(map[string]any)
	require.Equal(t, []any{"nci-social", "nci-technique"}, p["models"])
	require.Equal(t, "de", p["explanation_language"])
	require.Equal(t, "2026-11-1", p["glossary_version"])
	require.EqualValues(t, 11, p["detected_total"])
	got := p["detected"].([]any)
	require.Len(t, got, 8)
	var labels []any
	for _, d := range got {
		labels = append(labels, d.(map[string]any)["label"])
	}
	// T0 is best (0.95 from the second model), then T8..T3; T1's 0.99 is below its cutoff and T9/NoScore have none
	require.Equal(t, []any{"T0", "T8", "T7", "T6", "T5", "T4", "T3", "T2"}, labels)
	first := got[0].(map[string]any)
	require.Equal(t, "T0-de", first["name"])
	require.Equal(t, "Def T0", first["definition"])
	require.Equal(t, "Warum T0", first["why"])
	require.EqualValues(t, 0.95, first["score"])
	require.Equal(t, []any{"nci-social"}, first["models"])
	spans := first["spans"].([]any)
	require.Len(t, spans, 3, "at most 3 spans")
	for _, sp := range spans {
		m := sp.(map[string]any)
		require.Equal(t, m["text"], utf16Slice(content, int(m["start"].(float64)), int(m["end"].(float64))))
		require.Equal(t, "Weil "+m["text"].(string), m["why"])
	}
}

func TestTechniquesPreviewOlderResult(t *testing.T) {
	res := `{"results":[{"model":"nci-social","scores":[{"label":"Doubt","score":0.8,"above_cutoff":true}]}],
		"detected":[{"name":"Doubt","display":"Doubt","explanation":"E","models":["nci-social"],"spans":[{"text":"t","start":0,"end":1}]}]}`
	p := pv(t, "techniques", res).(map[string]any)
	d := p["detected"].([]any)[0].(map[string]any)
	require.Equal(t, "Doubt", d["label"])
	for _, k := range []string{"name", "definition", "why"} {
		require.Nil(t, d[k], k)
	}
	require.Nil(t, p["explanation_language"])
	require.Nil(t, p["glossary_version"])
	require.Nil(t, d["spans"].([]any)[0].(map[string]any)["why"])
	// wrong types
	p = pv(t, "techniques", `{"results":"x","detected":[3,{"name":"A","spans":"x","models":7}],"explanation_language":1}`).(map[string]any)
	require.Equal(t, []any{}, p["models"])
	require.Nil(t, p["explanation_language"])
	require.EqualValues(t, 2, p["detected_total"])
	require.Len(t, p["detected"], 1)
	require.Equal(t, []any{}, p["detected"].([]any)[0].(map[string]any)["spans"])
	require.Nil(t, p["detected"].([]any)[0].(map[string]any)["score"])
}

func TestLLMTechniquesPreview(t *testing.T) {
	title := "Das TÖTET tausende"
	s, e := u16(title, "TÖTET tausende")
	f := func(label string, conf float64, field, text string, s, e int) string {
		return fmt.Sprintf(`{"label":%q,"source":"llm","name":"N-%s","definition":"D","explanation":"Ex %s","confidence":%v,"span":{"field":%q,"text":%q,"start":%d,"end":%d}}`,
			label, label, label, conf, field, text, s, e)
	}
	fs := []string{f("misleading_headline", 0.7, "title", "TÖTET tausende", s, e), f("Loaded_Language", 0.99, "content", "x", 0, 1),
		f("statistical_cherry_picking", 0.9, "content", "y", 2, 3), f("evidence_certainty_mismatch", 0.7, "content", "z", 4, 5)}
	for i := 0; i < 5; i++ {
		fs = append(fs, f("evidence_certainty_mismatch", 0.1+float64(i)/100, "content", "q", 0, 1))
	}
	res := `{"source":"llm","title_checked":true,"labels_checked":["a"],"explanation_language":"de","glossary_version":"v","truncated":false,"findings":[` + strings.Join(fs, ",") + `]}`
	p := pv(t, "llm_techniques", res).(map[string]any)
	require.Equal(t, "llm", p["source"])
	require.Equal(t, true, p["title_checked"])
	require.Equal(t, "de", p["explanation_language"])
	require.EqualValues(t, 9, p["findings_total"])
	got := p["findings"].([]any)
	require.Len(t, got, 6, "max 6")
	var order []any
	for _, x := range got {
		m := x.(map[string]any)
		require.Equal(t, "llm", m["source"])
		require.Contains(t, []any{"evidence_certainty_mismatch", "misleading_headline", "statistical_cherry_picking"}, m["label"])
		order = append(order, m["label"])
	}
	require.Equal(t, []any{"statistical_cherry_picking", "misleading_headline", "evidence_certainty_mismatch"}, order[:3], "confidence desc, ties in result order")
	head := got[1].(map[string]any)
	sp := head["span"].(map[string]any)
	require.Equal(t, "title", sp["field"])
	require.Equal(t, sp["text"], utf16Slice(title, int(sp["start"].(float64)), int(sp["end"].(float64))))
	require.Equal(t, "Ex misleading_headline", head["explanation"])
	require.EqualValues(t, 0.7, head["confidence"])

	p = pv(t, "llm_techniques", `{"findings":[{"label":"misleading_headline"},"x"],"title_checked":"y"}`).(map[string]any)
	require.Nil(t, p["title_checked"])
	f0 := p["findings"].([]any)[0].(map[string]any)
	require.Nil(t, f0["span"])
	require.Nil(t, f0["confidence"])
	require.EqualValues(t, 2, p["findings_total"])
}

func TestPreviewsNeverMergeLLMIntoTechniques(t *testing.T) {
	p := pv(t, "techniques", `{"results":[],"detected":[{"name":"misleading_headline","models":[]}]}`).(map[string]any)
	require.Len(t, p["detected"], 1) // the techniques preview shows what its own result has, nothing is added from llm_techniques
	require.NotContains(t, p, "findings")
}

func TestPreviewPanicYieldsNullAndJobCompletes(t *testing.T) {
	prev := previewBuilders["translation"]
	previewBuilders["translation"] = func(map[string]any) any { panic("boom") }
	defer func() { previewBuilders["translation"] = prev }()
	require.Nil(t, buildPreview("translation", raw(`{}`)))

	e := newEngine(t, regYAML(an("translation", "ok")), Config{}, map[string]adapter.Handler{"ok": ok})
	jb, sub := createWithEvents(t, e, in("x"))
	evs := drain(t, sub)
	require.Equal(t, job.StatusCompleted, waitDone(t, e, jb.ID).Status)
	for _, ev := range evs {
		if ev.Type == "analyzer.succeeded" {
			require.Contains(t, data(ev), "preview")
			require.Nil(t, data(ev)["preview"])
			require.NotNil(t, data(ev)["progress"])
		}
	}
}

func TestPreviewHasNoUsageOrForeignMembers(t *testing.T) {
	p := pv(t, "topic_extraction", `{"claims":[],"topics":[],"usage":{"total_tokens":5},"attempts":1}`).(map[string]any)
	require.NotContains(t, p, "usage")
	require.ElementsMatch(t, []string{"claims", "topics", "claims_total", "topics_total"}, keys(p))
}

func keys(m map[string]any) (out []string) {
	for k := range m {
		out = append(out, k)
	}
	return
}
