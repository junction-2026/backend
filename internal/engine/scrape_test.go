package engine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/scraper"
	ss "github.com/junction-2026/backend/internal/socialscraper"
)

type moduleFunc func(ctx context.Context, t ss.Target, req ss.Request) (*ss.Result, error)

func (f moduleFunc) Scrape(ctx context.Context, t ss.Target, req ss.Request) (*ss.Result, error) {
	return f(ctx, t, req)
}

// stubScraper runs mod for every platform through the in-process scraper core.
func stubScraper(mod moduleFunc) *scraper.Client {
	reg := ss.NewRegistry()
	for _, p := range []string{ss.Bluesky, ss.Mastodon} {
		reg.Register(p, mod)
	}
	return &scraper.Client{Core: ss.New(reg, nil)}
}

func reply(res *ss.Result, err error) moduleFunc {
	return func(context.Context, ss.Target, ss.Request) (*ss.Result, error) { return res, err }
}

func scrapeReq(url string) CreateRequest {
	return CreateRequest{Scrape: &job.ScrapeRequest{URL: url, MaxPosts: 20, MaxComments: 50}}
}

// scrapeRegistry: `a` runs, `off` is disabled, `long` is inapplicable to short content.
var scrapeRegistry = regYAML(an("a", "ok"), an("off", "ok", "    enabled: false\n"),
	an("long", "ok", "    applies_to: {min_content_length: 1000}\n"), finDef)

func postResult() *ss.Result {
	return &ss.Result{Platform: "mastodon", URLKind: "post", Username: "alice@mastodon.social", DisplayName: ss.Ptr("Alice"),
		Following: ss.Ptr(3), Post: &ss.Post{ID: "1", Text: "Vote no!", CreatedAt: ss.Ptr("2026-11-13T09:00:00Z"), Likes: ss.Ptr(1), Replies: ss.Ptr(0)},
		Posts:    []ss.Post{{ID: "1", Text: "Vote no!"}},
		Warnings: []ss.Warning{{Code: "comments_unavailable", Message: "no comments"}}}
}

func TestScrapePostSuccess(t *testing.T) {
	var gotReq ss.Request
	var gotTarget ss.Target
	sc := stubScraper(func(_ context.Context, t ss.Target, req ss.Request) (*ss.Result, error) {
		gotReq, gotTarget = req, t
		return postResult(), nil
	})
	var seen map[string]any
	capture := func(_ context.Context, req adapter.Request) (any, error) {
		seen = req.Inputs
		return map[string]string{}, nil
	}
	f := &fakeFinalizer{}
	e := newEngine(t, scrapeRegistry, Config{Scraper: sc, Finalizer: f}, map[string]adapter.Handler{"ok": capture, "final": ok})

	url := "https://mastodon.social/@alice/1"
	created, sub := createWithEvents(t, e, scrapeReq(url))
	require.Equal(t, job.StatusQueued, created.Status)
	require.Nil(t, created.Input)
	require.Equal(t, job.Scrape{Status: job.RunPending, ScrapeRequest: job.ScrapeRequest{URL: url, MaxPosts: 20, MaxComments: 50},
		Warnings: []json.RawMessage{}}, *created.Scrape)
	require.Equal(t, job.RunPending, ar(created, "long").Status, "not_applicable waits for the scrape")
	require.Equal(t, job.ReasonDisabled, *ar(created, "off").Reason)

	evs := drain(t, sub)
	requireSeq(t, evs)
	require.Equal(t, []string{"job.snapshot", "job.started", "analyzer.skipped:off", "scrape.started", "scrape.succeeded", "analysis.planned",
		"analyzer.skipped:long", "analyzer.started:a", "analyzer.succeeded:a", "analysis.ready_for_finalization",
		"analysis.finalizing", "analysis.finalized", "job.completed"}, typesFor(evs))

	require.Equal(t, ss.Request{MaxPosts: 20, MaxComments: 50}, gotReq)
	require.Equal(t, url, gotTarget.URL.String())

	j := waitDone(t, e, created.ID)
	require.Equal(t, job.StatusCompleted, j.Status)
	in := j.Input
	require.Equal(t, "Vote no!", in.Content)
	require.Equal(t, "text/plain", in.ContentType)
	require.Equal(t, job.Source{URL: url, Title: "Alice", Author: "alice@mastodon.social", Platform: "mastodon",
		PublishedAt: job.Ptr(time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)), Metadata: in.Source.Metadata}, in.Source)
	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(in.Source.Metadata, &meta))
	var embedded ss.Result
	require.NoError(t, json.Unmarshal(meta["scrape"], &embedded))
	want := postResult()
	want.Comments, want.ScrapedAt, want.LatencyMS = []ss.Comment{}, embedded.ScrapedAt, embedded.LatencyMS
	require.Equal(t, *want, embedded, "the ScrapeResult is embedded as the scraper produced it")

	s := j.Scrape
	require.Equal(t, job.RunSucceeded, s.Status)
	require.Equal(t, "mastodon", *s.Platform)
	require.Equal(t, "post", *s.URLKind)
	require.Nil(t, s.Error)
	require.NotNil(t, s.FinishedAt)
	require.NotNil(t, s.DurationMS)
	require.Len(t, s.Warnings, 1)
	require.Contains(t, string(s.Warnings[0]), "comments_unavailable")
	require.Equal(t, job.ReasonNotApplicable, *ar(j, "long").Reason)

	// analyzers bound to $input see the mapped input
	require.Equal(t, "Vote no!", seen["content"].(map[string]any)["content"])
	require.Equal(t, 1, f.Calls())

	// events equal REST state
	for _, ev := range evs {
		d, _ := ev.Data.(map[string]any)
		switch ev.Type {
		case "scrape.started":
			require.Equal(t, job.RunRunning, d["scrape"].(job.Scrape).Status)
		case "scrape.succeeded":
			require.Equal(t, *j.Scrape, d["scrape"])
			require.Equal(t, *j.Input, d["input"])
		case "job.completed":
			require.Equal(t, j, d["job"])
		}
	}
}

func TestScrapeProfileHistoryAsContent(t *testing.T) {
	res := &ss.Result{Platform: "bluesky", URLKind: "profile", Username: "alice.bsky.social",
		Posts: []ss.Post{{ID: "3", Text: "B", CreatedAt: ss.Ptr("2026-11-13T09:00:00Z")}, {ID: "2"}, {ID: "1", Text: "A"}}}
	e := newEngine(t, scrapeRegistry, Config{Scraper: stubScraper(reply(res, nil))}, map[string]adapter.Handler{"ok": ok, "final": ok})
	j, err := e.Create(scrapeReq("https://bsky.app/profile/alice.bsky.social"))
	require.NoError(t, err)
	j = waitDone(t, e, j.ID)
	require.Equal(t, job.StatusCompleted, j.Status)
	require.Equal(t, "B\n\nA", j.Input.Content)
	require.Equal(t, "", j.Input.Source.Title)
	require.Nil(t, j.Input.Source.PublishedAt, "published_at only for post URLs")
	require.Equal(t, "profile", *j.Scrape.URLKind)
	require.Equal(t, []json.RawMessage{}, j.Scrape.Warnings, "no warnings: []")
}

// The full error mapping is tested in internal/scraper; here the engine settles each kind of failure.
func TestScrapeFailureMapping(t *testing.T) {
	for name, c := range map[string]struct {
		mod     moduleFunc
		code    string
		details string
	}{
		"account_private passes through": {mod: reply(nil, ss.AccountPrivate("mastodon", "private")), code: "account_private", details: `{"platform":"mastodon"}`},
		"panic":                          {mod: func(context.Context, ss.Target, ss.Request) (*ss.Result, error) { panic("boom") }, code: job.CodeScraperUnavailable},
		"invalid result":                 {mod: reply(&ss.Result{Platform: "mastodon"}, nil), code: job.CodeScraperUnavailable},
		"profile without text":           {mod: reply(&ss.Result{Platform: "mastodon", URLKind: "profile", Username: "a"}, nil), code: job.CodeNoTextContent},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeFinalizer{}
			e := newEngine(t, scrapeRegistry, Config{Scraper: stubScraper(c.mod), Finalizer: f}, map[string]adapter.Handler{"ok": ok, "final": ok})
			created, sub := createWithEvents(t, e, scrapeReq("https://mastodon.social/@alice"))
			evs := drain(t, sub)
			require.Equal(t, []string{"job.snapshot", "job.started", "analyzer.skipped:off", "scrape.started", "scrape.failed",
				"analyzer.skipped:a", "analyzer.skipped:long", "job.failed"}, typesFor(evs))

			j := waitDone(t, e, created.ID)
			require.Equal(t, job.StatusFailed, j.Status)
			require.Nil(t, j.Input)
			require.Equal(t, job.RunFailed, j.Scrape.Status)
			require.Equal(t, c.code, j.Scrape.Error.Code)
			if c.details != "" {
				require.JSONEq(t, c.details, string(j.Scrape.Error.Details))
			}
			require.Nil(t, j.Scrape.Platform)
			require.Equal(t, []json.RawMessage{}, j.Scrape.Warnings)
			for _, id := range []string{"a", "long"} {
				require.Equal(t, job.ReasonInputUnavailable, *ar(j, id).Reason)
			}
			require.Equal(t, job.ReasonDisabled, *ar(j, "off").Reason)
			require.Equal(t, job.RunSkipped, j.Finalization.Status)
			require.Equal(t, job.ReasonInputUnavailable, *j.Finalization.Reason)
			require.Zero(t, f.Calls(), "no finalizer and no insufficient-evidence assessment")
			require.Equal(t, j, evs[len(evs)-1].Data.(map[string]any)["job"])
			require.Equal(t, *j.Scrape, evs[4].Data.(map[string]any)["scrape"])
		})
	}
}

func TestScrapeTimeout(t *testing.T) {
	sc := stubScraper(func(ctx context.Context, _ ss.Target, _ ss.Request) (*ss.Result, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	e := newEngine(t, scrapeRegistry, Config{Scraper: sc, ScrapeTimeout: 50 * time.Millisecond}, map[string]adapter.Handler{"ok": ok, "final": ok})
	j, err := e.Create(scrapeReq("https://mastodon.social/@alice"))
	require.NoError(t, err)
	j = waitDone(t, e, j.ID)
	require.Equal(t, job.StatusFailed, j.Status)
	require.Equal(t, job.CodeTimeout, j.Scrape.Error.Code)
	require.Equal(t, job.ReasonInputUnavailable, *ar(j, "a").Reason)
}

func TestCancelWhileScraping(t *testing.T) {
	inflight, aborted := make(chan struct{}), make(chan struct{})
	sc := stubScraper(func(ctx context.Context, _ ss.Target, _ ss.Request) (*ss.Result, error) {
		close(inflight)
		<-ctx.Done()
		close(aborted)
		return nil, ctx.Err()
	})
	f := &fakeFinalizer{}
	e := newEngine(t, scrapeRegistry, Config{Scraper: sc, Finalizer: f}, map[string]adapter.Handler{"ok": ok, "final": ok})
	created, sub := createWithEvents(t, e, scrapeReq("https://mastodon.social/@alice"))
	<-inflight

	j, err := e.Get(created.ID)
	require.NoError(t, err)
	require.Equal(t, job.StatusScraping, j.Status)
	require.Nil(t, j.Input)
	require.Equal(t, job.RunRunning, j.Scrape.Status)
	require.Equal(t, "https://mastodon.social/@alice", j.Scrape.URL)
	require.Equal(t, job.RunPending, ar(j, "a").Status)
	require.Equal(t, job.RunPending, ar(j, "long").Status)

	j, err = e.Cancel(created.ID)
	require.NoError(t, err)
	require.Equal(t, job.StatusCancelled, j.Status)
	require.Nil(t, j.Input)
	require.Equal(t, job.RunFailed, j.Scrape.Status)
	require.Equal(t, job.CodeCancelled, j.Scrape.Error.Code)
	require.Equal(t, job.ReasonCancelled, *ar(j, "a").Reason)
	require.Equal(t, job.ReasonCancelled, *j.Finalization.Reason)
	select {
	case <-aborted:
	case <-time.After(5 * time.Second):
		t.Fatal("the scrape was not aborted")
	}
	evs := drain(t, sub)
	require.Equal(t, []string{"job.snapshot", "job.started", "analyzer.skipped:off", "scrape.started", "scrape.failed",
		"analyzer.skipped:a", "analyzer.skipped:long", "job.cancelled"}, typesFor(evs))
	require.Zero(t, f.Calls())
	got, _ := e.Get(created.ID)
	require.Equal(t, j, got, "a late scraper result does not change the cancelled job")
}

func TestCancelQueuedScrapeJob(t *testing.T) {
	called := make(chan struct{}, 1)
	sc := stubScraper(func(context.Context, ss.Target, ss.Request) (*ss.Result, error) {
		called <- struct{}{}
		return postResult(), nil
	})
	e := newEngine(t, scrapeRegistry, Config{Scraper: sc, MaxParallelJobs: 1}, map[string]adapter.Handler{"ok": block, "final": ok})
	first, err := e.Create(in("x")) // holds the only slot
	require.NoError(t, err)
	q, err := e.Create(scrapeReq("https://mastodon.social/@alice"))
	require.NoError(t, err)
	sub, err := e.Subscribe(q.ID)
	require.NoError(t, err)
	j, err := e.Cancel(q.ID)
	require.NoError(t, err)
	require.Equal(t, job.CodeCancelled, j.Scrape.Error.Code)
	evs := drain(t, sub)
	require.Equal(t, []string{"job.snapshot", "job.started", "analyzer.skipped:off", "scrape.failed",
		"analyzer.skipped:a", "analyzer.skipped:long", "job.cancelled"}, typesFor(evs), "no scrape.started for a scrape that never ran")
	_, _ = e.Cancel(first.ID)
	require.Len(t, called, 0)
}

func TestScrapeWithoutScraper(t *testing.T) {
	e := newEngine(t, scrapeRegistry, Config{}, map[string]adapter.Handler{"ok": ok, "final": ok})
	_, err := e.Create(scrapeReq("https://mastodon.social/@alice"))
	require.ErrorIs(t, err, ErrScrapeUnavailable)
	require.Empty(t, e.List(""))
}

// The default scrape timeout fits an unrendered page fetch, a rendered one (>= 150 s) and one 429/5xx retry.
func TestDefaultScrapeTimeout(t *testing.T) {
	e := newEngine(t, scrapeRegistry, Config{}, map[string]adapter.Handler{"ok": ok, "final": ok})
	require.Equal(t, 360*time.Second, e.cfg.ScrapeTimeout)
	require.Greater(t, e.cfg.ScrapeTimeout, (30+150+150)*time.Second)
}
