package socialscraper

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedact(t *testing.T) {
	r := NewRedactor(env(map[string]string{"SCRAPER_TOKEN": "scraper-tok-1"}), "extra-secret-2")

	in := "headers {'Authorization': 'Bearer abc.def'} Authorization: Bearer xyz scraper-tok-1 extra-secret-2"
	out := r.Redact(in)
	for _, secret := range []string{"abc.def", "xyz", "scraper-tok-1", "extra-secret-2"} {
		assert.NotContains(t, out, secret)
	}
	assert.Contains(t, out, "Bearer [redacted]")
	assert.Equal(t, "nothing to hide here", r.Redact("nothing to hide here"))
}

func TestLoggerRedactsMessagesAttrsErrorsAndTraces(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, NewRedactor(env(map[string]string{"SCRAPER_TOKEN": "tok-123456789"})))
	log.Info("Authorization: Bearer abc.def", "error", errors.New("failed tok-123456789"),
		"stack", "goroutine 1 [running]:\nmain.go:1 tok-123456789", "other", map[string]string{"k": "tok-123456789"}, "n", 3)
	out := buf.String()
	assert.NotContains(t, out, "abc.def")
	assert.NotContains(t, out, "tok-123456789")
	assert.Contains(t, out, "[redacted]")
	assert.Contains(t, out, `"n":3`)
	assert.Contains(t, out, `"level":"INFO"`)
}

func TestOxylabsCredentialsRedacted(t *testing.T) {
	r := NewRedactor(env(map[string]string{"OXYLABS_WEB_API_KEY": "key-123456", "OXYLABS_USERNAME": "user-abc", "OXYLABS_PASSWORD": "pw-xyz789"}))
	got := r.Redact("failed key-123456 as user-abc with pw-xyz789")
	assert.NotContains(t, got, "key-123456")
	assert.NotContains(t, got, "user-abc")
	assert.NotContains(t, got, "pw-xyz789")
}
