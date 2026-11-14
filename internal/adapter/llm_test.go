package adapter

import (
	"context"
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
	msg := providerError(401, []byte(`{"error":{"message":"`+strings.Repeat("x", 290)+key+`"}}`), key)
	require.NotContains(t, msg, "sk-")
	require.True(t, strings.HasPrefix(msg, "llm provider HTTP 401: "+strings.Repeat("x", 290)+"[REDACTE"))
	require.Equal(t, "llm provider HTTP 502", providerError(502, []byte("<html>bad gateway</html>"), key))
}

func TestDecodeReply(t *testing.T) {
	out, usage, err := decodeReply([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":" ` + "```json\\n{\\\"a\\\":1}\\n```" + ` "}}]}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"a":1}`, string(out))
	require.Nil(t, usage, "absent usage is null")
	for _, body := range []string{
		`not json`,
		`{"choices":[{"finish_reason":"content_filter","message":{"content":"{}"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"content":"[1]"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"content":"` + "```" + `"}}]}`,
	} {
		_, _, err := decodeReply([]byte(body))
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
