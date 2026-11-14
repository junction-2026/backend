package finalizer

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/scraper"
)

// After a successful scrape the finalizer rules are those of text jobs; after a
// failed scrape the finalizer is never called.
func TestFinalizerRulesForScrapeJobs(t *testing.T) {
	scraperCfg := func(status int, body string) engine.Config {
		sc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(sc.Close)
		return engine.Config{Scraper: &scraper.Client{URL: sc.URL, Token: "t"}}
	}
	s := newStub(t, reply(okBody))
	cfg := scraperCfg(200, `{"platform":"x","url_kind":"post","username":"a","post":{"id":"1","text":"hi"},"posts":[]}`)
	scrape := engine.CreateRequest{Scrape: &job.ScrapeRequest{URL: "https://x.com/a/status/1", MaxPosts: 20, MaxComments: 50}}

	// every analyzer fails: insufficient-evidence short-circuit, no finalizer call
	e := newEngine(t, an("bad1", "fail")+an("bad2", "fail")+finYAML(""), s, map[string]adapter.Handler{"fail": failHandler}, cfg)
	created, sub := create(t, e, scrape)
	drain(t, sub)
	j := waitDone(t, e, created.ID)
	require.Equal(t, job.StatusCompleted, j.Status)
	require.JSONEq(t, shortCircuit, string(j.Finalization.Result))
	require.Zero(t, s.calls.Load())

	// an analyzer succeeds: the finalizer runs on the mapped input
	e = newEngine(t, an("ok1", "ok")+finYAML(""), s, map[string]adapter.Handler{"ok": okHandler}, cfg)
	created, sub = create(t, e, scrape)
	drain(t, sub)
	j = waitDone(t, e, created.ID)
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	require.EqualValues(t, 1, s.calls.Load())
	require.Contains(t, string(s.bodies[0]), `"content":"hi"`)

	// failed scrape: job failed, finalization skipped, finalizer not called
	e = newEngine(t, an("ok1", "ok")+finYAML(""), s, map[string]adapter.Handler{"ok": okHandler},
		scraperCfg(403, `{"error":{"code":"account_private","message":"private"}}`))
	created, sub = create(t, e, scrape)
	evs := drain(t, sub)
	j = waitDone(t, e, created.ID)
	require.Equal(t, job.StatusFailed, j.Status)
	require.Equal(t, job.ReasonInputUnavailable, *j.Finalization.Reason)
	require.Zero(t, count(evs, "analysis.ready_for_finalization"))
	require.EqualValues(t, 1, s.calls.Load())
}
