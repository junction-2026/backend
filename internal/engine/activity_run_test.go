package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

func startedActivity(evs []events.Event, id string) job.Activity {
	for _, ev := range evs {
		if ev.Type == "analyzer.started" && data(ev)["analyzer"].(job.AnalyzerRun).ID == id {
			return data(ev)["activity"].(job.Activity)
		}
	}
	panic("no analyzer.started for " + id)
}

func TestFactCheckSkipEventAndActivity(t *testing.T) {
	e, _ := factCheckEngine(t, `[]`, 200)
	_, sub := createWithEvents(t, e, in("politicians are all the same, wake up"))
	evs := drain(t, sub)
	checkProgress(t, evs)
	for _, ev := range evs {
		if ev.Type == "analyzer.skipped" && data(ev)["analyzer"].(job.AnalyzerRun).ID == "fact_check" {
			d := data(ev)
			run := d["analyzer"].(job.AnalyzerRun)
			require.Equal(t, job.AvailNotApplicable, *d["availability"].(*string))
			require.Equal(t, "Not run: the text contains no specific factual claims to check.", *d["note"].(*string))
			require.Equal(t, run.Availability, d["availability"])
			require.Equal(t, run.Note, d["note"])
			require.NotNil(t, d["progress"])
		}
	}
	require.Equal(t, map[string]any{"claims": 0}, startedActivity(evs, "fact_check").Params)

	e, _ = factCheckEngine(t, `[{"text":"A.","language":"en","query":"A","query_en":"A","entities":[],"check_worthiness":0.9},`+
		`{"text":"B.","language":"en","query":"B","query_en":"B","entities":[],"check_worthiness":0.8},`+
		`{"text":"C.","language":"en","query":"C","query_en":"C","entities":[],"check_worthiness":0.1}]`, 403)
	_, sub = createWithEvents(t, e, in("A. B. C."))
	act := startedActivity(drain(t, sub), "fact_check")
	require.Equal(t, map[string]any{"claims": 2}, act.Params)
	require.Equal(t, "for 2 claims", *act.Detail)
}

// The techniques activity names exactly the models the run used.
func TestTechniquesActivityMatchesRun(t *testing.T) {
	clf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Text []string }
		json.NewDecoder(r.Body).Decode(&in)
		l := []any{}
		for _, n := range registry.TechniqueNames {
			l = append(l, map[string]any{"label": n, "score": 0.1})
		}
		json.NewEncoder(w).Encode(map[string]any{"result": []any{l}})
	}))
	defer clf.Close()
	var text string
	mis := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, _ := json.Marshal(map[string]any{"source_language": "en", "translated_text": text})
		reply, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(content)}}}})
		w.Write(reply)
	}))
	defer mis.Close()
	yaml := fmt.Sprintf("analyzers:\n"+
		"  - id: translation\n    execution: {adapter: mistral_translation, endpoint: %q, api_key: sk-secret-key, skip_english: false}\n"+
		"  - id: techniques\n    depends_on: [translation]\n    input: {bindings: {text: $analyzers.translation}}\n"+
		"    execution:\n      adapter: technique_classifier\n      technique: {endpoint: %q}\n      social: {endpoint: %q}\n", mis.URL, clf.URL, clf.URL)
	e := newEngine(t, yaml, Config{}, nil)
	for _, words := range []int{5, 40, 81} {
		text = strings.TrimSpace(strings.Repeat("word ", words))
		j, sub := createWithEvents(t, e, in(text))
		evs := drain(t, sub)
		checkProgress(t, evs)
		var res struct{ Results []struct{ Model string } }
		require.NoError(t, json.Unmarshal(ar(waitDone(t, e, j.ID), "techniques").Result, &res))
		var used []string
		for _, r := range res.Results {
			used = append(used, r.Model)
		}
		require.Equal(t, map[string]any{"models": used}, startedActivity(evs, "techniques").Params, "%d words", words)
		for _, ev := range evs { // the succeeded event carries the preview built from the result
			if ev.Type == "analyzer.succeeded" && data(ev)["analyzer"].(job.AnalyzerRun).ID == "techniques" {
				require.Equal(t, used, data(ev)["preview"].(map[string]any)["models"])
			}
		}
	}
}
