package engine

import (
	"encoding/json"
	"fmt"
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
