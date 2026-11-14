package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// TestTechniqueAfterTranslation runs translation -> techniques end to end against stub services.
func TestTechniqueAfterTranslation(t *testing.T) {
	var calls atomic.Int32
	clf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var in struct{ Text []string }
		json.NewDecoder(r.Body).Decode(&in)
		lists := []any{}
		for range in.Text {
			l := []any{}
			for _, n := range registry.TechniqueNames {
				l = append(l, map[string]any{"label": n, "score": map[bool]float64{true: 0.9, false: 0.1}[n == "Doubt"]})
			}
			lists = append(lists, l)
		}
		json.NewEncoder(w).Encode(map[string]any{"result": lists})
	}))
	defer clf.Close()
	mistralStatus := 200
	mis := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(mistralStatus)
		content, _ := json.Marshal(map[string]any{"source_language": "en", "translated_text": "Wake up. They are LYING to you."})
		reply, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(content)}}}})
		w.Write(reply)
	}))
	defer mis.Close()
	yaml := fmt.Sprintf("analyzers:\n"+
		"  - id: translation\n    execution: {adapter: mistral_translation, endpoint: %q, api_key: sk-secret-key}\n"+
		"  - id: techniques\n    depends_on: [translation]\n    input: {bindings: {text: $analyzers.translation}}\n"+
		"    execution:\n      adapter: technique_classifier\n      technique: {endpoint: %q}\n      social: {endpoint: %q}\n",
		mis.URL, clf.URL, clf.URL)
	e := newEngine(t, yaml, Config{}, nil)

	j, err := e.Create(in("Wake up. They are LYING to you."))
	require.NoError(t, err)
	done := waitDone(t, e, j.ID)
	tr := ar(done, "techniques")
	require.Equal(t, job.RunSucceeded, tr.Status)
	var res struct {
		Words   int
		Results []struct {
			Model  string
			Scores []struct{ Label string }
		}
		Detected []struct {
			Name   string
			Models []string
		}
	}
	require.NoError(t, json.Unmarshal(tr.Result, &res))
	require.Equal(t, 7, res.Words)
	require.Len(t, res.Results, 1) // 7 words: nci-social only
	require.Equal(t, "nci-social", res.Results[0].Model)
	require.Len(t, res.Results[0].Scores, 18)
	require.Len(t, res.Detected, 1)
	require.Equal(t, "Doubt", res.Detected[0].Name)
	require.Equal(t, []string{"nci-social"}, res.Detected[0].Models)
	require.Equal(t, int32(1), calls.Load())

	// translation fails: techniques is skipped and no classifier request is sent
	mistralStatus = 401
	j, err = e.Create(in("Wake up. They are LYING to you."))
	require.NoError(t, err)
	done = waitDone(t, e, j.ID)
	require.Equal(t, job.RunFailed, ar(done, "translation").Status)
	sk := ar(done, "techniques")
	require.Equal(t, job.RunSkipped, sk.Status)
	require.Equal(t, job.ReasonDependencyFailed, *sk.Reason)
	require.Nil(t, sk.Result)
	require.Equal(t, int32(1), calls.Load())
}

// TestSkippedEnglishTranslationFeedsConsumers: an English job gets a skipped translation result (no Mistral request),
// and techniques and propaganda_detection use it like a Mistral result; spans are offsets into Job.input.content.
func TestSkippedEnglishTranslationFeedsConsumers(t *testing.T) {
	const content = "They are LYING to you! Only we tell the truth. Wake up before it's too late, patriots!"
	var mistralCalls atomic.Int32
	mis := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mistralCalls.Add(1)
		w.WriteHeader(500)
	}))
	defer mis.Close()
	var propBody atomic.Value
	prop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		propBody.Store(string(b))
		w.Write([]byte(`{"result":[{"label":"PROPAGANDA","score":0.8}],"latency_ms":1}`))
	}))
	defer prop.Close()
	var clfTexts atomic.Value
	scorer := func(record bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in struct{ Text []string }
			json.NewDecoder(r.Body).Decode(&in)
			if record {
				clfTexts.Store(in.Text)
			}
			lists := []any{}
			for range in.Text {
				l := []any{}
				for _, n := range registry.TechniqueNames {
					l = append(l, map[string]any{"label": n, "score": map[bool]float64{true: 0.9, false: 0.1}[n == "Doubt"]})
				}
				lists = append(lists, l)
			}
			json.NewEncoder(w).Encode(map[string]any{"result": lists})
		}
	}
	clf := httptest.NewServer(scorer(true))
	defer clf.Close()
	sent := httptest.NewServer(scorer(false))
	defer sent.Close()
	yaml := fmt.Sprintf("analyzers:\n"+
		"  - id: translation\n    execution: {adapter: mistral_translation, endpoint: %q, api_key: sk-secret-key}\n"+
		"  - id: propaganda_detection\n    depends_on: [translation]\n    input: {bindings: {text: $analyzers.translation}}\n"+
		"    execution: {adapter: propaganda_detection, endpoint: %q}\n"+
		"  - id: techniques\n    depends_on: [translation]\n    input: {bindings: {text: $analyzers.translation}}\n"+
		"    execution:\n      adapter: technique_classifier\n      technique: {endpoint: %q}\n      social: {endpoint: %q}\n"+
		"      spans:\n        timeout: 5s\n        sentence_model: {endpoint: %q, threshold: 0.5}\n"+
		"        llm: {base_url: \"http://127.0.0.1:1/v1\", api_key: k, model: m, prompt_file: ../../config/technique_spans/prompt.md}\n",
		mis.URL, prop.URL, clf.URL, clf.URL, sent.URL)
	e := newEngine(t, yaml, Config{}, nil)

	j, err := e.Create(in(content))
	require.NoError(t, err)
	done := waitDone(t, e, j.ID)
	require.Zero(t, mistralCalls.Load())
	tr := ar(done, "translation")
	require.Equal(t, job.RunSucceeded, tr.Status)
	var trRes map[string]any
	require.NoError(t, json.Unmarshal(tr.Result, &trRes))
	require.Equal(t, "detected_english", trRes["skipped"])
	require.Equal(t, "en", trRes["source_language"])
	require.Equal(t, content, trRes["translated_text"])

	require.Equal(t, job.RunSucceeded, ar(done, "propaganda_detection").Status)
	b, _ := json.Marshal(map[string]string{"text": content})
	require.JSONEq(t, string(b), propBody.Load().(string))

	tech := ar(done, "techniques")
	require.Equal(t, job.RunSucceeded, tech.Status)
	require.Equal(t, []string{content}, clfTexts.Load())
	var res struct {
		SpanMethod string `json:"span_method"`
		SpanStatus string `json:"span_status"`
		Detected   []struct {
			Spans []struct {
				Text       string
				Start, End int
			}
		}
	}
	require.NoError(t, json.Unmarshal(tech.Result, &res))
	require.Equal(t, "sentence", res.SpanMethod)
	require.Equal(t, "ok", res.SpanStatus)
	require.NotEmpty(t, res.Detected)
	require.NotEmpty(t, res.Detected[0].Spans)
	for _, s := range res.Detected[0].Spans {
		require.Equal(t, s.Text, content[s.Start:s.End]) // ASCII: UTF-16 offsets are byte offsets
	}
}
