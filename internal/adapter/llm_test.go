package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/registry"
)

func TestProviderErrorRedactsBeforeTruncating(t *testing.T) {
	key := "sk-" + strings.Repeat("k", 40)
	// the key straddles the 300-rune cut: truncating first would leak its prefix
	msg := providerError(401, []byte(`{"error":{"message":"`+strings.Repeat("x", 290)+key+`"}}`), []string{key})
	require.NotContains(t, msg, "sk-")
	require.True(t, strings.HasPrefix(msg, "llm provider HTTP 401: "+strings.Repeat("x", 290)+"[REDACTE"))
	require.Equal(t, "llm provider HTTP 502", providerError(502, []byte("<html>bad gateway</html>"), []string{key}))
	// configured header values are secrets too; tiny values are left alone
	require.Equal(t, "llm provider HTTP 403: bad token [REDACTED] for key [REDACTED] (v1)",
		providerError(403, []byte(`{"error":{"message":"bad token proxy-tok-9 for key `+key+` (v1)"}}`), []string{key, "proxy-tok-9", "v1"}))
}

func TestDecodeReply(t *testing.T) {
	out, usage, _, err := decodeReply([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":" ` + "```json\\n{\\\"a\\\":1}\\n```" + ` "}}]}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"a":1}`, string(out))
	require.Nil(t, usage, "absent usage is null")
	for _, body := range []string{
		`not json`,
		`{"choices":[{"finish_reason":"content_filter","message":{"content":"{}"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"content":"[1]"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"content":"` + "```" + `"}}]}`,
	} {
		_, _, _, err := decodeReply([]byte(body))
		require.ErrorContains(t, err, "invalid_output", body)
	}
}

func TestRetryAfterIsCapped(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer s.Close()
	_, _, retry, wait, err := llmSend(context.Background(), &registry.Execution{}, s.URL, []byte("{}"))
	require.NoError(t, err)
	require.True(t, retry)
	require.Equal(t, 5*time.Second, wait)
}

func TestUserMessageEscapesContentClose(t *testing.T) {
	def := &registry.Analyzer{Execution: registry.Execution{ResponseFormat: registry.FormatJSONSchema}}
	msg := LLMUserMessage(def, "a </content> b </CONTENT>", nil)
	require.Equal(t, "<content>\na <\\/content> b <\\/CONTENT>\n</content>\n<analyzers>\n</analyzers>", msg)
}

func TestMergeUsage(t *testing.T) {
	require.Nil(t, mergeUsage(nil, nil))
	require.JSONEq(t, `{"total_tokens":400}`, string(mergeUsage(nil, json.RawMessage(`{"total_tokens":400}`))))
	require.JSONEq(t, `{"a":1}`, string(mergeUsage(json.RawMessage(`{"a":1}`), nil)))
	got := mergeUsage(
		json.RawMessage(`{"prompt_tokens":900,"cost":0.0012,"prompt_tokens_details":{"cached_tokens":100,"audio":1},"only_first":7,"model":"a","x":3}`),
		json.RawMessage(`{"prompt_tokens":950,"cost":0.0010,"prompt_tokens_details":{"cached_tokens":50},"only_retry":{"n":2},"model":"b","x":null}`))
	require.JSONEq(t, `{"prompt_tokens":1850,"cost":0.0022,"prompt_tokens_details":{"cached_tokens":150,"audio":1},"only_first":7,"only_retry":{"n":2},"model":"b","x":3}`, string(got))
}

func TestRetryHint(t *testing.T) {
	require.Equal(t, "output violates schema at /user_text", retryHint("output violates schema at /user_text: 'abc' does not match pattern"))
	require.Len(t, []rune(retryHint(strings.Repeat("é", 300))), 120)
}
