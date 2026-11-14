package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/facebook"
	"github.com/junction-2026/backend/internal/socialscraper/scrapertest"
	"github.com/junction-2026/backend/internal/socialscraper/x"
)

const (
	sentinelAuth = "SENTINEL-auth-6f1c9d2b"
	sentinelCt0  = "SENTINEL-ct0-8a7e3b5d"
)

func TestRegisteredXAnswersNotConfiguredWithoutCredentials(t *testing.T) {
	t.Setenv("TWITTER_AUTH_TOKEN", "")
	t.Setenv("TWITTER_CT0", "")
	ss.Default = ss.NewRegistry()
	registerPlatforms(ss.Config{Token: "tok", Timeout: time.Second})
	rec := post(t, ss.NewServer(ss.Config{Token: "tok", Timeout: time.Second}, ss.Default, nil), "https://x.com/alice")
	assert.Equal(t, 503, rec.Code)
	assert.Contains(t, rec.Body.String(), `"missing_settings":["TWITTER_AUTH_TOKEN","TWITTER_CT0"]`)
}

func post(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/scrape", strings.NewReader(`{"url":"`+target+`"}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// fbBrowser is a scripted facebook.Browser.
type fbBrowser struct {
	body     string
	navErr   error
	navPanic string
	closed   *int
}

func (b *fbBrowser) Navigate(string) (int, error) {
	if b.navPanic != "" {
		panic(b.navPanic)
	}
	return 200, b.navErr
}
func (b *fbBrowser) Scroll() error    { return nil }
func (b *fbBrowser) Bodies() [][]byte { return [][]byte{[]byte(b.body)} }
func (b *fbBrowser) Text() string     { return "" }
func (b *fbBrowser) Close()           { *b.closed++ }

type panicRT struct{}

func (panicRT) RoundTrip(*http.Request) (*http.Response, error) {
	panic("transport exploded with " + sentinelAuth + " and " + sentinelCt0)
}

const xUser = `{"data":{"user":{"result":{"__typename":"User","rest_id":"1","core":{"screen_name":"alice","name":"Alice"},"legacy":{"followers_count":1}}}}}`

// TestCredentialsNeverLeak runs successful, failing and panicking X and Facebook scrapes with sentinel
// credentials through the real server and redacting logger: no sentinel may reach a response, a header or a log.
func TestCredentialsNeverLeak(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{"TWITTER_AUTH_TOKEN": sentinelAuth, "TWITTER_CT0": sentinelCt0, "SCRAPER_TOKEN": "tok"}[k]
	}
	var logs bytes.Buffer
	log := ss.NewLogger(&logs, ss.NewRedactor(getenv, nil))
	prev := slog.Default()
	slog.SetDefault(log) // module warnings go through slog.Default
	t.Cleanup(func() { slog.SetDefault(prev) })

	leak := "upstream echoed " + sentinelAuth + " " + sentinelCt0
	xOK := func(w http.ResponseWriter, r *http.Request) {
		body := xUser
		if strings.HasSuffix(r.URL.Path, "/UserTweets") {
			body = `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[]}}}}}}`
		}
		scrapertest.Body(200, []byte(body))(w, r)
	}
	xFail := scrapertest.Body(500, []byte(leak))
	fbPage := `{"data":{"page":{"__typename":"Page","id":"1","name":"Example","url":"https://www.facebook.com/examplenews","followers_count":3}}}`

	type scenario struct {
		name   string
		target string
		want   int
		mod    func(t *testing.T) (ss.Module, *int)
	}
	xMod := func(rt http.RoundTripper) func(*testing.T) (ss.Module, *int) {
		return func(*testing.T) (ss.Module, *int) { return x.New(ss.NewClient(5*time.Second, rt), getenv), nil }
	}
	fbMod := func(b *fbBrowser) func(*testing.T) (ss.Module, *int) {
		return func(*testing.T) (ss.Module, *int) {
			closed := 0
			b.closed = &closed
			return &facebook.Module{Launch: func(context.Context) (facebook.Browser, error) { return b, nil }}, &closed
		}
	}
	scenarios := []scenario{
		{"x success", "https://x.com/alice", 200, xMod(scrapertest.New(t).Handle("x.com", http.HandlerFunc(xOK)))},
		{"x failure", "https://x.com/alice", 502, xMod(scrapertest.New(t).Handle("x.com", xFail))},
		{"x transport failure", "https://x.com/alice", 502, xMod(scrapertest.New(t).Fail("x.com", errors.New(leak)))},
		{"x panic", "https://x.com/alice", 500, xMod(panicRT{})},
		{"facebook success", "https://www.facebook.com/examplenews", 200, fbMod(&fbBrowser{body: fbPage})},
		{"facebook failure", "https://www.facebook.com/examplenews", 502, fbMod(&fbBrowser{navErr: errors.New(leak)})},
		{"facebook panic", "https://www.facebook.com/examplenews", 500, fbMod(&fbBrowser{navPanic: leak})},
	}
	var everything strings.Builder
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			mod, closed := sc.mod(t)
			reg := ss.NewRegistry()
			reg.Register(ss.Twitter, mod)
			reg.Register(ss.Facebook, mod)
			rec := post(t, ss.NewServer(ss.Config{Token: "tok", Timeout: 5 * time.Second}, reg, log), sc.target)
			require.Equal(t, sc.want, rec.Code, rec.Body.String())
			fmt.Fprintf(&everything, "%s\n%v\n", rec.Body.String(), rec.Header())
			if closed != nil {
				assert.Equal(t, 1, *closed, "the browser is closed")
			}
		})
	}
	// the panics were really logged (redacted), so the log check below is not vacuous
	assert.Contains(t, logs.String(), "module panicked")
	assert.Contains(t, logs.String(), "[redacted]")
	everything.WriteString(logs.String())
	assert.NotContains(t, everything.String(), sentinelAuth)
	assert.NotContains(t, everything.String(), sentinelCt0)
}
