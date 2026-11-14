package finalizer

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
)

const topicDesc = "Fact-checkable topics and news found in the text."

// topicYAML: topic_extraction (an llm analyzer opted into finalizer input) on the same stub as the finalizer.
func topicYAML() string {
	return "  - id: topic_extraction\n    execution:\n      adapter: llm\n      task: topic_extraction\n" +
		"      base_url: ${FINALIZER_BASE_URL}\n      api_key: ${FINALIZER_API_KEY}\n      model: ${FINALIZER_MODEL}\n      prompt_file: ${PROMPT}\n" +
		"    llm: {include: true, description: \"" + topicDesc + "\"}\n"
}

// extractorStub answers the finalizer (its user message ends the <analyzers> block; the prompt only mentions the opening tag) with a verdict and the extractor with extraction.
func extractorStub(t *testing.T, extraction string) *stub {
	var s *stub
	s = newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock() // the stub already stored this request's body
		b := string(s.bodies[len(s.bodies)-1])
		s.mu.Unlock()
		if strings.Contains(b, "\\n</analyzers>") {
			io.WriteString(w, chat(verdictOK))
			return
		}
		io.WriteString(w, chat(extraction))
	})
	return s
}

func TestTopicExtractionReachesFinalizerWhenOptedIn(t *testing.T) {
	s := extractorStub(t, `{"topics":[{"title":"T","query":"q"}],"news":[],"claims":[]}`)
	x := newLLMEngine(t, llmAnalyzers()+topicYAML()+llmFinYAML(""), s)
	j := runsOf(t, x.e, engine.CreateRequest{Input: job.Input{Content: "text"}, Analyzers: []string{"pd", "topic_extraction"}})
	require.Equal(t, job.StatusCompleted, j.Status)
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	require.EqualValues(t, 2, s.calls.Load(), "one extraction call, one finalizer call")

	r, _ := lastReq(t, s)
	user := r.Messages[1].Content
	require.Contains(t, user, `{"id":"topic_extraction","description":"`+topicDesc+`","status":"succeeded","result":{"topics":[{"title":"T","query":"q","description":null}],"news":[],"claims":[],"input_truncated":false,"usage":`+stubUsage+`}}`)
	require.Contains(t, user, `"id":"pd"`)
}

func TestInvalidExtractionFailsOnlyThatAnalyzer(t *testing.T) {
	s := extractorStub(t, `plain prose`)
	x := newLLMEngine(t, llmAnalyzers()+topicYAML()+llmFinYAML(""), s)
	j := runsOf(t, x.e, engine.CreateRequest{Input: job.Input{Content: "text"}, Analyzers: []string{"pd", "topic_extraction"}})
	require.Equal(t, job.StatusCompleted, j.Status)
	require.Equal(t, job.StatusCompleted, j.Finalization.Status, "%+v", j.Finalization.Error)
	byID := map[string]job.AnalyzerRun{}
	for _, a := range j.Analyzers {
		byID[a.ID] = a
	}
	require.Equal(t, job.RunFailed, byID["topic_extraction"].Status)
	require.Equal(t, job.CodeInvalidOutput, byID["topic_extraction"].Error.Code)
	require.Nil(t, byID["topic_extraction"].Result)
	require.Equal(t, job.RunSucceeded, byID["pd"].Status)
	r, _ := lastReq(t, s)
	require.Contains(t, r.Messages[1].Content, `{"id":"topic_extraction","description":"`+topicDesc+`","status":"failed","reason":"invalid_output"}`)
}

func TestTopicExtractionNotOptedInIsHidden(t *testing.T) {
	s := extractorStub(t, `{"topics":[],"news":[],"claims":[]}`)
	y := strings.Replace(topicYAML(), "    llm: {include: true, description: \""+topicDesc+"\"}\n", "", 1)
	x := newLLMEngine(t, llmAnalyzers()+y+llmFinYAML(""), s)
	j := runsOf(t, x.e, engine.CreateRequest{Input: job.Input{Content: "text"}, Analyzers: []string{"pd", "topic_extraction"}})
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	r, _ := lastReq(t, s)
	require.NotContains(t, r.Messages[1].Content, "topic_extraction")
}
