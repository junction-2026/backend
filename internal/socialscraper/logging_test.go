package socialscraper

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedact(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "session.json")
	require.NoError(t, os.WriteFile(session, []byte(`{"sessionid":"sess-1234567890","csrftoken":"csrf-abcdefghij","ds_user_id":"42"}`), 0o600))
	r := NewRedactor(env(map[string]string{
		"SCRAPER_TOKEN": "scraper-tok-1", "TWITTER_AUTH_TOKEN": "tw-auth-2", "TWITTER_CT0": "tw-ct0-3", "INSTAGRAM_SESSION_FILE": session,
	}), os.ReadFile)

	in := "headers {'Authorization': 'Bearer abc.def'} Authorization: Bearer xyz scraper-tok-1 tw-auth-2 tw-ct0-3 " +
		session + " sess-1234567890 csrf-abcdefghij"
	out := r.Redact(in)
	for _, secret := range []string{"abc.def", "xyz", "scraper-tok-1", "tw-auth-2", "tw-ct0-3", session, "sess-1234567890", "csrf-abcdefghij"} {
		assert.NotContains(t, out, secret)
	}
	assert.Contains(t, out, "Bearer [redacted]")
	assert.Equal(t, "nothing to hide here", r.Redact("nothing to hide here"))
}

func TestLoggerRedactsMessagesAttrsErrorsAndTraces(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, NewRedactor(env(map[string]string{"SCRAPER_TOKEN": "tok-123456789"}), nil))
	log.Info("Authorization: Bearer abc.def", "error", errors.New("failed tok-123456789"),
		"stack", "goroutine 1 [running]:\nmain.go:1 tok-123456789", "other", map[string]string{"k": "tok-123456789"}, "n", 3)
	out := buf.String()
	assert.NotContains(t, out, "abc.def")
	assert.NotContains(t, out, "tok-123456789")
	assert.Contains(t, out, "[redacted]")
	assert.Contains(t, out, `"n":3`)
	assert.Contains(t, out, `"level":"INFO"`)
}
