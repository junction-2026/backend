package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/chat"
)

func cfgOf(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestChatConfigDefaults(t *testing.T) {
	c, err := loadConfig(cfgOf(nil))
	require.NoError(t, err)
	require.Equal(t, chat.Config{BaseURL: "http://127.0.0.1:8642/v1", Model: "hermes-agent", Timeout: 90 * time.Second, RatePerMinute: 10, MaxConcurrent: 4}, c.Chat)
}

func TestChatConfigValues(t *testing.T) {
	file := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(file, []byte("  from-file\n"), 0o600))
	c, err := loadConfig(cfgOf(map[string]string{"CHAT_BASE_URL": "https://agent.example/v1", "CHAT_MODEL": "m", "CHAT_API_KEY": "from-env",
		"CHAT_API_KEY_FILE": file, "CHAT_TIMEOUT": "5s", "CHAT_RATE_PER_MINUTE": "3", "CHAT_MAX_CONCURRENT": "2"}))
	require.NoError(t, err)
	require.Equal(t, chat.Config{BaseURL: "https://agent.example/v1", Model: "m", APIKey: "from-file", Timeout: 5 * time.Second, RatePerMinute: 3, MaxConcurrent: 2}, c.Chat, "the file wins")

	c, err = loadConfig(cfgOf(map[string]string{"CHAT_API_KEY": " from-env "}))
	require.NoError(t, err)
	require.Equal(t, "from-env", c.Chat.APIKey)
}

func TestChatConfigErrorsNameTheVariable(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"CHAT_BASE_URL":        {"CHAT_BASE_URL": "ftp://s3cr3tvalue"},
		"CHAT_TIMEOUT":         {"CHAT_TIMEOUT": "s3cr3tvalue"},
		"CHAT_RATE_PER_MINUTE": {"CHAT_RATE_PER_MINUTE": "0"},
		"CHAT_MAX_CONCURRENT":  {"CHAT_MAX_CONCURRENT": "-s3cr3tvalue"},
		"CHAT_API_KEY_FILE":    {"CHAT_API_KEY_FILE": "/nonexistent/s3cr3tvalue"},
	} {
		_, err := loadConfig(cfgOf(vars))
		require.ErrorContains(t, err, name)
		require.NotContains(t, err.Error(), "s3cr3tvalue", name)
	}
	_, err := loadConfig(cfgOf(map[string]string{"CHAT_TIMEOUT": "0s"}))
	require.ErrorContains(t, err, "CHAT_TIMEOUT")
}

func completedJobID(t *testing.T, s *server) string {
	t.Helper()
	code, created := s.json(t, "POST", "/api/v1/jobs", `{"input":{"content":"one two\nthree"}}`)
	require.Equal(t, 201, code)
	id := created["id"].(string)
	require.Eventually(t, func() bool { _, j := s.json(t, "GET", "/api/v1/jobs/"+id, ""); return j["done"] == true }, 10*time.Second, 10*time.Millisecond)
	return id
}

func TestChatWithoutKeyStartsAndAnswers503(t *testing.T) {
	logs := &lockedBuf{}
	s := startLog(t, map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers"}, logs)
	id := completedJobID(t, s)
	code, body := s.json(t, "POST", "/api/v1/jobs/"+id+"/chat", `{"messages":[{"role":"user","content":"hi"}]}`)
	require.Equal(t, 503, code)
	require.Equal(t, "chat_unavailable", body["error"].(map[string]any)["code"])
}

func TestChatEndToEndKeyFileNeverLogged(t *testing.T) {
	const key = "sk-e2e-file-key-0123456789"
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+key, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "Three words."}}}})
	}))
	defer agent.Close()
	file := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(file, []byte(key+"\n"), 0o600))
	logs := &lockedBuf{}
	s := startLog(t, map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers", "CHAT_BASE_URL": agent.URL, "CHAT_API_KEY_FILE": file}, logs)
	id := completedJobID(t, s)
	code, body := s.json(t, "POST", "/api/v1/jobs/"+id+"/chat", `{"messages":[{"role":"user","content":"How many words?"}]}`)
	require.Equal(t, 200, code)
	require.Equal(t, "Three words.", body["message"].(map[string]any)["content"])
	require.NotContains(t, logs.String(), key)
	require.Contains(t, logs.String(), `"job_id"`)
}

// lockedBuf is a log sink safe for the server goroutines and the test.
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
