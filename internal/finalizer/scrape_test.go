package finalizer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/scraper"
	ss "github.com/junction-2026/backend/internal/socialscraper"
)

type scrapeModule func() (*ss.Result, error)

func (f scrapeModule) Scrape(context.Context, ss.Target, ss.Request) (*ss.Result, error) { return f() }

// After a successful scrape the finalizer rules are those of text jobs; after a
// failed scrape the finalizer is never called.
func TestFinalizerRulesForScrapeJobs(t *testing.T) {
	scraperCfg := func(res *ss.Result, err error) engine.Config {
		reg := ss.NewRegistry()
		reg.Register(ss.Twitter, scrapeModule(func() (*ss.Result, error) { return res, err }))
		return engine.Config{Scraper: &scraper.Client{Core: ss.New(reg, nil)}}
	}
	s := newStub(t, reply(okBody))
	cfg := scraperCfg(&ss.Result{Platform: "twitter", URLKind: "post", Username: "a", Post: &ss.Post{ID: "1", Text: "hi"}}, nil)
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
		scraperCfg(nil, ss.AccountPrivate("twitter", "private")))
	created, sub = create(t, e, scrape)
	evs := drain(t, sub)
	j = waitDone(t, e, created.ID)
	require.Equal(t, job.StatusFailed, j.Status)
	require.Equal(t, job.ReasonInputUnavailable, *j.Finalization.Reason)
	require.Zero(t, count(evs, "analysis.ready_for_finalization"))
	require.EqualValues(t, 1, s.calls.Load())
}
