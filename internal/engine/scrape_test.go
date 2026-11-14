package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/scraper"
)

const scrapeToken = "s3cret-scraper-token"

// stubScraper serves h as the scraper service.
func stubScraper(t *testing.T, h http.HandlerFunc) *scraper.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &scraper.Client{URL: srv.URL, Token: scrapeToken}
}

func reply(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func scrapeReq(url string) CreateRequest {
	return CreateRequest{Scrape: &job.ScrapeRequest{URL: url, MaxPosts: 20, MaxComments: 50}}
}

// scrapeRegistry: `a` runs, `off` is disabled, `long` is inapplicable to short content.
var scrapeRegistry = regYAML(an("a", "ok"), an("off", "ok", "    enabled: false\n"),
	an("long", "ok", "    applies_to: {min_content_length: 1000}\n"), finDef)

const postResult = `{"platform":"mastodon","url_kind":"post","username":"alice@mastodon.social","display_name":"Alice",
"bio":null,"profile_url":null,"followers":null,"following":3,"post_count":null,"verified":null,
"post":{"id":"1","text":"Vote no!","created_at":"2026-11-13T09:00:00Z","url":null,"likes":1,"replies":0,"reposts":null},
"posts":[{"id":"1","text":"Vote no!","created_at":null,"url":null,"likes":null,"replies":null,"reposts":null}],
"comments":[],"warnings":[{"code":"comments_unavailable","message":"no comments"}],
"scraped_at":"2026-11-14T10:00:00Z","latency_ms":12,"lang":"en"}`

func TestScrapePostSuccess(t *testing.T) {
	var gotAuth, gotBody, gotPath string
	sc := stubScraper(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotAuth, gotBody, gotPath = r.Header.Get("Authorization"), string(b), r.URL.Path
		reply(200, postResult)(w, r)
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
	require.Equal(t, []string{"job.snapshot", "job.started", "analyzer.skipped:off", "scrape.started", "scrape.succeeded",
		"analyzer.skipped:long", "analyzer.started:a", "analyzer.succeeded:a", "analysis.ready_for_finalization",
		"analysis.finalizing", "analysis.finalized", "job.completed"}, typesFor(evs))

	require.Equal(t, "/scrape", gotPath)
	require.Equal(t, "Bearer "+scrapeToken, gotAuth)
	require.JSONEq(t, `{"url":"`+url+`","max_posts":20,"max_comments":50}`, gotBody)

	j := waitDone(t, e, created.ID)
	require.Equal(t, job.StatusCompleted, j.Status)
	in := j.Input
	require.Equal(t, "Vote no!", in.Content)
	require.Equal(t, "text/plain", in.ContentType)
	require.Equal(t, job.Source{URL: url, Title: "Alice", Author: "alice@mastodon.social", Platform: "mastodon",
		PublishedAt: job.Ptr(time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)), Metadata: in.Source.Metadata}, in.Source)
	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(in.Source.Metadata, &meta))
	require.JSONEq(t, postResult, string(meta["scrape"]), "ScrapeResult verbatim, unknown members included")

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
	body := `{"platform":"bluesky","url_kind":"profile","username":"alice.bsky.social","display_name":null,
"post":null,"comments":[],"posts":[{"id":"3","text":"B","created_at":"2026-11-13T09:00:00Z"},{"id":"2","text":""},{"id":"1","text":"A"}],
"scraped_at":"2026-11-14T10:00:00Z","latency_ms":5}`
	e := newEngine(t, scrapeRegistry, Config{Scraper: stubScraper(t, reply(200, body))}, map[string]adapter.Handler{"ok": ok, "final": ok})
	j, err := e.Create(scrapeReq("https://bsky.app/profile/alice.bsky.social"))
	require.NoError(t, err)
	j = waitDone(t, e, j.ID)
	require.Equal(t, job.StatusCompleted, j.Status)
	require.Equal(t, "B\n\nA", j.Input.Content)
	require.Equal(t, "", j.Input.Source.Title)
	require.Nil(t, j.Input.Source.PublishedAt, "published_at only for post URLs")
	require.Equal(t, "profile", *j.Scrape.URLKind)
	require.Equal(t, []json.RawMessage{}, j.Scrape.Warnings, "missing warnings default to []")
}

func TestScrapeFailureMapping(t *testing.T) {
	envelope := func(code, details string) string {
		return `{"error":{"code":"` + code + `","message":"scraper says ` + code + `"` + details + `}}`
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	for name, c := range map[string]struct {
		h       http.HandlerFunc
		url     string // scraper base URL override (unreachable)
		code    string
		details string
	}{
		"account_private passes through": {h: reply(403, envelope("account_private", `,"details":{"platform":"instagram"}`)), code: "account_private", details: `{"platform":"instagram"}`},
		"unsupported_url":                {h: reply(422, envelope("unsupported_url", "")), code: "unsupported_url"},
		"account_not_found":              {h: reply(404, envelope("account_not_found", `,"details":{"platform":"bluesky"}`)), code: "account_not_found", details: `{"platform":"bluesky"}`},
		"rate_limited":                   {h: reply(429, envelope("rate_limited", `,"details":{"retry_after_s":30}`)), code: "rate_limited", details: `{"retry_after_s":30}`},
		"platform_not_configured":        {h: reply(503, envelope("platform_not_configured", `,"details":{"missing_settings":["TWITTER_CT0"]}`)), code: "platform_not_configured", details: `{"missing_settings":["TWITTER_CT0"]}`},
		"unknown scraper code":           {h: reply(418, envelope("new_code", "")), code: "new_code"},
		"401 unauthorized":               {h: reply(401, envelope("unauthorized", "")), code: job.CodeScraperUnavailable},
		"invalid_request":                {h: reply(400, envelope("invalid_request", "")), code: job.CodeScraperUnavailable},
		"validation_failed":              {h: reply(422, envelope("validation_failed", "")), code: job.CodeScraperUnavailable},
		"500 internal_error":             {h: reply(500, envelope("internal_error", "")), code: job.CodeScraperUnavailable},
		"error without envelope":         {h: reply(502, "Bad Gateway"), code: job.CodeScraperUnavailable},
		"200 not an object":              {h: reply(200, `[]`), code: job.CodeScraperUnavailable},
		"200 null":                       {h: reply(200, `null`), code: job.CodeScraperUnavailable},
		"200 missing url_kind":           {h: reply(200, `{"platform":"x","posts":[]}`), code: job.CodeScraperUnavailable},
		"200 post kind without post":     {h: reply(200, `{"platform":"x","url_kind":"post","post":null,"posts":[]}`), code: job.CodeScraperUnavailable},
		"unreachable":                    {url: closed.URL, code: job.CodeScraperUnavailable},
		"post without text":              {h: reply(200, `{"platform":"instagram","url_kind":"post","username":"a","post":{"id":"p","text":"  "},"posts":[]}`), code: job.CodeNoTextContent},
		"profile without text":           {h: reply(200, `{"platform":"x","url_kind":"profile","username":"a","post":null,"posts":[{"id":"p","text":""}]}`), code: job.CodeNoTextContent},
	} {
		t.Run(name, func(t *testing.T) {
			sc := &scraper.Client{URL: c.url, Token: scrapeToken}
			if c.h != nil {
				sc = stubScraper(t, c.h)
			}
			f := &fakeFinalizer{}
			e := newEngine(t, scrapeRegistry, Config{Scraper: sc, Finalizer: f}, map[string]adapter.Handler{"ok": ok, "final": ok})
			created, sub := createWithEvents(t, e, scrapeReq("https://x.com/alice"))
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
			require.NotContains(t, j.Scrape.Error.Message, scrapeToken)
			require.NotContains(t, j.Scrape.Error.Message, "127.0.0.1")
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

func TestScrapeErrorMessageRedacted(t *testing.T) {
	var sc *scraper.Client
	sc = stubScraper(t, func(w http.ResponseWriter, r *http.Request) {
		reply(502, `{"error":{"code":"upstream_error","message":"failed via `+sc.URL+` with `+scrapeToken+`"}}`)(w, r)
	})
	e := newEngine(t, scrapeRegistry, Config{Scraper: sc}, map[string]adapter.Handler{"ok": ok, "final": ok})
	j, err := e.Create(scrapeReq("https://x.com/alice"))
	require.NoError(t, err)
	j = waitDone(t, e, j.ID)
	require.Equal(t, "upstream_error", j.Scrape.Error.Code)
	require.NotContains(t, j.Scrape.Error.Message, scrapeToken)
	require.NotContains(t, j.Scrape.Error.Message, sc.URL)
}

func TestScrapeTimeout(t *testing.T) {
	sc := stubScraper(t, func(_ http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() })
	e := newEngine(t, scrapeRegistry, Config{Scraper: sc, ScrapeTimeout: 50 * time.Millisecond}, map[string]adapter.Handler{"ok": ok, "final": ok})
	j, err := e.Create(scrapeReq("https://x.com/alice"))
	require.NoError(t, err)
	j = waitDone(t, e, j.ID)
	require.Equal(t, job.StatusFailed, j.Status)
	require.Equal(t, job.CodeTimeout, j.Scrape.Error.Code)
	require.Equal(t, job.ReasonInputUnavailable, *ar(j, "a").Reason)
}

func TestCancelWhileScraping(t *testing.T) {
	inflight, aborted := make(chan struct{}), make(chan struct{})
	sc := stubScraper(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(inflight)
		<-r.Context().Done()
		close(aborted)
	})
	f := &fakeFinalizer{}
	e := newEngine(t, scrapeRegistry, Config{Scraper: sc, Finalizer: f}, map[string]adapter.Handler{"ok": ok, "final": ok})
	created, sub := createWithEvents(t, e, scrapeReq("https://x.com/alice"))
	<-inflight

	j, err := e.Get(created.ID)
	require.NoError(t, err)
	require.Equal(t, job.StatusScraping, j.Status)
	require.Nil(t, j.Input)
	require.Equal(t, job.RunRunning, j.Scrape.Status)
	require.Equal(t, "https://x.com/alice", j.Scrape.URL)
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
		t.Fatal("the scraper request was not aborted")
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
	sc := stubScraper(t, func(w http.ResponseWriter, r *http.Request) { called <- struct{}{}; reply(200, postResult)(w, r) })
	e := newEngine(t, scrapeRegistry, Config{Scraper: sc, MaxParallelJobs: 1}, map[string]adapter.Handler{"ok": block, "final": ok})
	first, err := e.Create(in("x")) // holds the only slot
	require.NoError(t, err)
	q, err := e.Create(scrapeReq("https://x.com/alice"))
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
	_, err := e.Create(scrapeReq("https://x.com/alice"))
	require.ErrorIs(t, err, ErrScrapeUnavailable)
	require.Empty(t, e.List(""))
}
