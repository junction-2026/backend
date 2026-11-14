package finalizer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// The rewritten verdict rules of config/finalizer/prompt.md (parent criteria 2 to 4).
func TestPromptStrongSignalRules(t *testing.T) {
	b, err := os.ReadFile(promptFile(t))
	require.NoError(t, err)
	p := strings.Join(strings.Fields(string(b)), " ")
	for _, w := range []string{
		// strong-signal list
		`A strong signal is any of:`, `a "techniques" detection with "strength": "strong"`,
		`an "llm_techniques" finding with "confidence" of 0.8 or more`, `a fact-check whose rating contradicts a claim in the text`,
		`news coverage that clearly contradicts a claim in the text`, `"propaganda_detection" with the label PROPAGANDA`,
		// verdict rules
		`"detected": at least one strong signal that the content supports.`,
		`"not_detected": no strong signal. Weak technique detections and low-confidence findings only lower your confidence; they, like not_applicable analyzers, are never a reason for "uncertain".`,
		`"uncertain": strong signals disagree, or an analyzer that applies to this text and would have been decisive is unavailable.`,
		// confidence guidance
		`"not_detected" with no weak hints: 0.8 or more; with some weak hints: 0.6 to 0.8.`,
		`"detected": higher the more strong signals there are and the more they agree.`,
		// consistency rule
		`"user_text" must agree with the verdict: never say the content looks normal or harmless under "uncertain"`,
		// unchanged availability rules
		`It must not lower your confidence and must not by itself lead to "uncertain".`,
	} {
		require.Contains(t, p, w)
	}
	require.NotContains(t, p, "evidence is weak, mixed or contradictory")
	require.NotContains(t, p, `Prefer "uncertain"`)
}

// classifierScores answers a technique model request with the given scores (others 0.1).
func classifierScores(t *testing.T, sc map[string]float64) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := []any{}
		for _, n := range registry.TechniqueNames {
			v, ok := sc[n]
			if !ok {
				v = 0.1
			}
			l = append(l, map[string]any{"label": n, "score": v})
		}
		json.NewEncoder(w).Encode(map[string]any{"result": []any{l}})
	}))
	t.Cleanup(s.Close)
	return s
}

// techniquesExampleYAML is docs/examples/technique_classifier.yaml without its spans block, pointed at clf.
func techniquesExampleYAML(t *testing.T, clf string) string {
	b, err := os.ReadFile("../../docs/examples/technique_classifier.yaml")
	require.NoError(t, err)
	_, y, ok := strings.Cut(string(b), "analyzers:\n")
	require.True(t, ok)
	i, k := strings.Index(y, "      spans:\n"), strings.Index(y, "    # Opts this")
	require.True(t, i > 0 && k > i)
	y = y[:i] + y[k:]
	return strings.NewReplacer("${NCI_TECHNIQUE_URL}", clf, "${NCI_SOCIAL_URL}", clf, "${ML_API_TOKEN}", "tok").Replace(y)
}

// Parent criterion 4: only weak technique detections and NO_PROPAGANDA reach the finalizer, every Detection weak.
func TestFinalizerInputWeakDetectionsOnly(t *testing.T) {
	clf := classifierScores(t, map[string]float64{"Bandwagon": 0.34, "Doubt": 0.4, "Flag-Waving": 0.31}) // social cutoff 0.3
	prop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"result":[{"label":"NO_PROPAGANDA","score":0.97}],"latency_ms":1}`)
	}))
	t.Cleanup(prop.Close)
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, chat(`{"verdict":"not_detected","confidence":0.7,"user_text":"No signs of manipulation."}`))
	})
	yaml := "  - id: translation\n    execution: {adapter: mistral_translation, endpoint: http://127.0.0.1:1, api_key: k}\n" +
		"  - id: propaganda_detection\n    depends_on: [translation]\n    input: {bindings: {text: $analyzers.translation}}\n" +
		"    execution: {adapter: propaganda_detection, endpoint: \"" + prop.URL + "\"}\n" +
		"    llm: {include: true, description: \"" + pdDesc + "\"}\n" +
		techniquesExampleYAML(t, clf.URL) + llmFinYAML("")
	x := newLLMEngine(t, yaml, s)
	const content = "We are happy to announce that our community meetup is moving to the new library on Friday evening."
	j := runsOf(t, x.e, engine.CreateRequest{Input: job.Input{Content: content}})
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	for _, a := range j.Analyzers {
		require.Equal(t, job.RunSucceeded, a.Status, a.ID)
	}

	r, _ := lastReq(t, s)
	require.Contains(t, r.Messages[0].Content, "A strong signal is any of:")
	var entry struct {
		Description string
		Result      struct {
			Detected []struct {
				Name     string
				Margin   *float64
				Located  *bool
				Strength string
			}
			Summary map[string]int
		}
	}
	for _, line := range strings.Split(r.Messages[1].Content, "\n") {
		if strings.HasPrefix(line, `{"id":"techniques"`) {
			require.NoError(t, json.Unmarshal([]byte(line), &entry))
		}
	}
	require.Contains(t, entry.Description, "must not move a verdict away from not_detected")
	require.Len(t, entry.Result.Detected, 3)
	for _, d := range entry.Result.Detected {
		require.NotNil(t, d.Margin, d.Name)
		require.NotNil(t, d.Located, d.Name)
		require.False(t, *d.Located, d.Name)
		require.Equal(t, "weak", d.Strength, d.Name)
	}
	require.Equal(t, map[string]int{"strong": 0, "weak": 3}, entry.Result.Summary)
	require.Contains(t, r.Messages[1].Content, `"label":"NO_PROPAGANDA"`)
}
