package finalizer

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
)

// llmTechniquesYAML is docs/examples/llm_techniques.yaml pointed at the test stub, plus a stand-in translation.
func llmTechniquesYAML(t *testing.T) string {
	b, err := os.ReadFile("../../docs/examples/llm_techniques.yaml")
	require.NoError(t, err)
	_, y, ok := strings.Cut(string(b), "analyzers:\n")
	require.True(t, ok)
	y = strings.NewReplacer("${LLM_TECHNIQUES_BASE_URL}", "${FINALIZER_BASE_URL}", "${LLM_TECHNIQUES_API_KEY}", "${FINALIZER_API_KEY}",
		"${LLM_TECHNIQUES_MODEL}", "${FINALIZER_MODEL}", "/etc/orchestrator/llm_techniques/prompt.md", "${PROMPT}").Replace(y)
	return "  - id: translation\n    execution: {adapter: custom, handler: ok}\n" + y
}

// techniquesStub answers the finalizer with a verdict and llm_techniques with llm (nil: HTTP 503).
func techniquesStub(t *testing.T, findings string) *stub {
	var s *stub
	s = newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		b := string(s.bodies[len(s.bodies)-1])
		s.mu.Unlock()
		switch {
		case strings.Contains(b, "\\n</analyzers>"):
			io.WriteString(w, chat(verdictOK))
		case findings == "":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			io.WriteString(w, chat(findings))
		}
	})
	return s
}

func TestLLMTechniquesReachFinalizerWithWeakerDescription(t *testing.T) {
	s := techniquesStub(t, `{"findings":[{"label":"statistical_cherry_picking","field":"content","text":"rose 300%","confidence":0.8,"explanation":"One month only."}]}`)
	x := newLLMEngine(t, llmAnalyzers()+llmTechniquesYAML(t)+llmFinYAML(""), s)
	j := runsOf(t, x.e, engine.CreateRequest{Input: job.Input{Content: "Crime rose 300% in May."}, Analyzers: []string{"pd", "llm_techniques"}})
	require.Equal(t, job.StatusCompleted, j.Status)
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)

	r, _ := lastReq(t, s)
	user := r.Messages[1].Content
	require.Contains(t, user, `{"id":"llm_techniques","description":"LLM-assessed findings`)
	for _, w := range []string{"assessed by an LLM", "weaker evidence than the techniques model labels", "must not be treated as model detections"} {
		require.Contains(t, user, w)
	}
	require.Contains(t, user, `"status":"succeeded","availability":"ran","result":{"source":"llm","title_checked":false`)
	require.Contains(t, user, `"span":{"field":"content","text":"rose 300%","start":6,"end":15}`)
}

func TestLLMTechniquesFailureIsolated(t *testing.T) {
	s := techniquesStub(t, "")
	x := newLLMEngine(t, llmAnalyzers()+llmTechniquesYAML(t)+llmFinYAML(""), s)
	j := runsOf(t, x.e, engine.CreateRequest{Input: job.Input{Content: "Crime rose 300% in May."}, Analyzers: []string{"pd", "llm_techniques"}})
	require.Equal(t, job.StatusCompleted, j.Status)
	byID := map[string]job.AnalyzerRun{}
	for _, a := range j.Analyzers {
		byID[a.ID] = a
	}
	require.Equal(t, job.RunFailed, byID["llm_techniques"].Status)
	require.Equal(t, job.CodeAdapterError, byID["llm_techniques"].Error.Code)
	require.Nil(t, byID["llm_techniques"].Result)
	require.Equal(t, job.RunSucceeded, byID["pd"].Status)
	require.NotContains(t, byID["llm_techniques"].Error.Message, testKey)
}
