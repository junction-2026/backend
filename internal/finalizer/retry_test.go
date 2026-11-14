package finalizer

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
)

const lengthText = "Your previous reply was cut off. Reply again with only the JSON object, and keep user_text within the word limit."

// replyWith is a provider answer with the given finish_reason, content and usage ("" = no usage member).
func replyWith(finish, content, usage string) string {
	c, _ := json.Marshal(content)
	u := ""
	if usage != "" {
		u = `,"usage":` + usage
	}
	return `{"choices":[{"finish_reason":"` + finish + `","message":{"role":"assistant","content":` + string(c) + `}}]` + u + `}`
}

// seq answers the i-th request with bodies[i] (the last one repeated).
func seq(bodies ...string) func(http.ResponseWriter, *http.Request) {
	var n atomic.Int32
	return func(w http.ResponseWriter, _ *http.Request) {
		i := min(int(n.Add(1))-1, len(bodies)-1)
		io.WriteString(w, bodies[i])
	}
}

// defaultLog captures slog.Default (the adapter logs retries there) for the test.
func defaultLog(t *testing.T) *lockedBuf {
	var b lockedBuf
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&b, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &b
}

type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func reqN(t *testing.T, s *stub, i int) (chatReq, string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Greater(t, len(s.bodies), i)
	var r chatReq
	require.NoError(t, json.Unmarshal(s.bodies[i], &r))
	return r, string(s.bodies[i])
}

// shared "Cut-off reply retried": usage summed, attempts 2, retry_reason length; retry_max_tokens used.
func TestLLMReplyRetryLength(t *testing.T) {
	logs := defaultLog(t)
	s := newStub(t, seq(
		replyWith("length", `{"verdict":"detec`, `{"prompt_tokens":900,"completion_tokens":600,"total_tokens":1500,"cost":0.0012}`),
		replyWith("stop", `{"verdict":"detected","confidence":0.7,"user_text":"..."}`, `{"prompt_tokens":950,"completion_tokens":300,"total_tokens":1250,"cost":0.0010}`)))
	x := newLLMEngine(t, llmAnalyzers()+llmFinYAML("      max_tokens: 700\n      retry_max_tokens: 1500\n"), s)
	j := runsOf(t, x.e, selected("x"))
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	require.JSONEq(t, `{"verdict":"detected","confidence":0.7,"user_text":"...","produced_by":"finalizer","attempts":2,"retry_reason":"length",`+
		`"usage":{"prompt_tokens":1850,"completion_tokens":900,"total_tokens":2750,"cost":0.0022},"wikipedia_quotes":[]}`, string(j.Finalization.Result))
	require.EqualValues(t, 2, s.calls.Load())

	first, _ := reqN(t, s, 0)
	second, _ := reqN(t, s, 1)
	require.Equal(t, 700, *first.MaxTokens)
	require.Equal(t, 1500, *second.MaxTokens)
	require.Equal(t, "gpt-test", second.Model)
	require.Len(t, second.Messages, 3)
	require.Equal(t, first.Messages, second.Messages[:2], "original messages repeated")
	require.Equal(t, struct{ Role, Content string }{"user", lengthText}, second.Messages[2])

	log := logs.String()
	require.Contains(t, log, "llm reply retry")
	require.Contains(t, log, "analyzer_id=final_synthesis")
	require.Contains(t, log, "reason=length")
	require.Contains(t, log, "model=gpt-test")
	require.NotContains(t, log, "detec\"")
}

// shared "Prose reply retried": invalid_json, reason quoted, first reply never echoed.
func TestLLMReplyRetryInvalidJSON(t *testing.T) {
	logs := defaultLog(t)
	prose := "I think this post is propaganda because SECRET-REPLY-TEXT."
	s := newStub(t, seq(replyWith("stop", prose, stubUsage), chat(verdictOK)))
	x := newLLMEngine(t, llmAnalyzers()+llmFinYAML(""), s)
	j := runsOf(t, x.e, selected("x"))
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	var res job.FinalAssessment
	require.NoError(t, json.Unmarshal(j.Finalization.Result, &res))
	require.Equal(t, 2, *res.Attempts)
	require.Equal(t, "invalid_json", *res.RetryReason)
	require.JSONEq(t, `{"prompt_tokens":1024,"completion_tokens":96,"total_tokens":1120}`, string(res.Usage))

	second, raw := reqN(t, s, 1)
	last := second.Messages[len(second.Messages)-1]
	require.Equal(t, "user", last.Role)
	const pre, post = "Your previous reply was not a valid JSON object matching the required schema (", "). Reply again with only that JSON object."
	require.True(t, strings.HasPrefix(last.Content, pre) && strings.HasSuffix(last.Content, post), last.Content)
	reason := strings.TrimSuffix(strings.TrimPrefix(last.Content, pre), post)
	require.Equal(t, "llm reply content is not a JSON object", reason)
	require.LessOrEqual(t, len([]rune(reason)), 120)
	require.NotContains(t, raw, "SECRET-REPLY-TEXT")
	require.NotContains(t, logs.String()+x.log.String(), "SECRET-REPLY-TEXT")
}

// shared "Schema mismatch retried".
func TestLLMReplyRetrySchema(t *testing.T) {
	s := newStub(t, seq(chat(`{"verdict":"detected","confidence":0.9}`), chat(verdictOK)))
	x := newLLMEngine(t, llmAnalyzers()+llmFinYAML(""), s)
	j := runsOf(t, x.e, selected("x"))
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	require.Contains(t, string(j.Finalization.Result), `"attempts":2`)
	require.Contains(t, string(j.Finalization.Result), `"retry_reason":"schema"`)
	second, _ := reqN(t, s, 1)
	require.Contains(t, second.Messages[2].Content, `(finalizer output member "user_text" is missing, not a string or empty)`)

	// a configured output.schema failure is a schema retry too; the hint stops before any quoted value
	s = newStub(t, seq(chat(`{"verdict":"detected","confidence":0.9,"user_text":"short"}`), chat(verdictOK)))
	x = newLLMEngine(t, llmAnalyzers()+llmFinYAML("")+"    output:\n      schema: {type: object, properties: {user_text: {type: string, minLength: 10}}}\n", s)
	j = runsOf(t, x.e, selected("x"))
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	require.Contains(t, string(j.Finalization.Result), `"retry_reason":"schema"`)
	second, _ = reqN(t, s, 1)
	require.Contains(t, second.Messages[2].Content, "(output violates schema at /user_text)")
}

// shared "First reply valid" and "Only the retry reported usage".
func TestLLMReplyRetryAttemptsAndUsage(t *testing.T) {
	logs := defaultLog(t)
	s := newStub(t, reply(chat(verdictOK)))
	x := newLLMEngine(t, llmAnalyzers()+llmFinYAML(""), s)
	j := runsOf(t, x.e, selected("x"))
	require.Contains(t, string(j.Finalization.Result), `"attempts":1,"retry_reason":null`)
	require.NotContains(t, logs.String(), "llm reply retry")

	s = newStub(t, seq(replyWith("stop", "nope", ""), replyWith("stop", verdictOK, `{"total_tokens":400}`)))
	x = newLLMEngine(t, llmAnalyzers()+llmFinYAML(""), s)
	j = runsOf(t, x.e, selected("x"))
	var res job.FinalAssessment
	require.NoError(t, json.Unmarshal(j.Finalization.Result, &res))
	require.JSONEq(t, `{"total_tokens":400}`, string(res.Usage))
	require.Equal(t, 2, *res.Attempts)
}

// retry_model: the retry uses that model on the same base_url and api_key.
func TestLLMReplyRetryModel(t *testing.T) {
	logs := defaultLog(t)
	s := newStub(t, seq(chat("prose"), chat(verdictOK)))
	x := newLLMEngine(t, llmAnalyzers()+llmFinYAML("      retry_model: gpt-big\n"), s)
	j := runsOf(t, x.e, selected("x"))
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	first, _ := reqN(t, s, 0)
	second, _ := reqN(t, s, 1)
	require.Equal(t, "gpt-test", first.Model)
	require.Equal(t, "gpt-big", second.Model)
	require.Equal(t, "Bearer "+testKey, s.header.Get("Authorization"), "same api_key; the stub is the same base_url")
	require.Contains(t, logs.String(), "model=gpt-big")
}

// No retry: disabled, too little time left, refusal, content_filter, HTTP 400.
func TestLLMReplyRetryExclusions(t *testing.T) {
	for name, c := range map[string]struct {
		h     func(http.ResponseWriter, *http.Request)
		extra string
		code  string
	}{
		"disabled":       {reply(chat("prose")), "      retry_invalid_output: false\n", job.CodeInvalidOutput},
		"little time":    {reply(chat("prose")), "      timeout: 2s\n      retry_min_remaining: 5s\n", job.CodeInvalidOutput},
		"refusal":        {reply(`{"choices":[{"finish_reason":"stop","message":{"content":null,"refusal":"I can't help"}}]}`), "", job.CodeInvalidOutput},
		"content_filter": {reply(replyWith("content_filter", "", "")), "", job.CodeInvalidOutput},
		"http 400": {func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"bad"}}`)
		}, "", job.CodeAdapterError},
	} {
		t.Run(name, func(t *testing.T) {
			logs := defaultLog(t)
			s := newStub(t, c.h)
			x := newLLMEngine(t, llmAnalyzers()+llmFinYAML(c.extra), s)
			j := runsOf(t, x.e, selected("x"))
			require.Equal(t, job.StatusCompleted, j.Status)
			require.Equal(t, job.RunFailed, j.Finalization.Status)
			require.Equal(t, c.code, j.Finalization.Error.Code)
			require.Nil(t, j.Finalization.Result)
			require.EqualValues(t, 1, s.calls.Load())
			require.NotContains(t, logs.String(), "llm reply retry")
		})
	}
}

// shared "Both replies invalid": invalid_output naming both reasons, analyzers keep their results.
func TestLLMReplyRetryBothInvalid(t *testing.T) {
	s := newStub(t, seq(replyWith("length", "{", stubUsage), chat("still prose")))
	x := newLLMEngine(t, llmAnalyzers()+llmFinYAML(""), s)
	j := runsOf(t, x.e, selected("x"))
	require.Equal(t, job.StatusCompleted, j.Status)
	require.Equal(t, job.RunFailed, j.Finalization.Status)
	require.Equal(t, job.CodeInvalidOutput, j.Finalization.Error.Code)
	require.Equal(t, `first: llm reply finish_reason is "length", not "stop"; retry: llm reply content is not a JSON object`, j.Finalization.Error.Message)
	require.Nil(t, j.Finalization.Result)
	require.EqualValues(t, 2, s.calls.Load())
	for _, r := range j.Analyzers {
		if r.ID == "pd" {
			require.Equal(t, job.RunSucceeded, r.Status)
		}
	}
}

// shared "Output members named attempts and retry_reason": the http finalizer's members are dropped.
func TestHTTPFinalizerAttemptsDropped(t *testing.T) {
	s := newStub(t, reply(`{"verdict":"detected","user_text":"...","attempts":5,"retry_reason":"x"}`))
	j, _ := run(t, s, "", req("x"))
	require.JSONEq(t, `{"verdict":"detected","confidence":null,"user_text":"...","produced_by":"finalizer","usage":null,"attempts":null,"retry_reason":null,"wikipedia_quotes":[]}`, string(j.Finalization.Result))
}
