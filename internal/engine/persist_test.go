package engine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/job"
	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/store"
)

// sqliteEngine is newEngine on the SQLite store at path (a restart is a second call with the same path).
func sqliteEngine(t *testing.T, path, yaml string, cfg Config, handlers map[string]adapter.Handler) (*Engine, *store.SQLite) {
	t.Helper()
	st, err := store.OpenSQLite(path)
	require.NoError(t, err)
	cfg.Store = st
	e := newEngine(t, yaml, cfg, handlers)
	t.Cleanup(func() { _ = st.Close() })
	return e, st
}

// crash stops the engine and the store the way a killed process leaves the database.
func crash(t *testing.T, e *Engine, st *store.SQLite) {
	t.Helper()
	e.Close()
	require.NoError(t, st.Close())
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// snapshotOf reads the only event of a closed stream.
func snapshotOf(t *testing.T, e *Engine, id string) events.Event {
	t.Helper()
	sub, err := e.Subscribe(id)
	require.NoError(t, err)
	evs := drain(t, sub)
	require.Len(t, evs, 1, "exactly one job.snapshot, then the stream ends")
	require.NoError(t, sub.Err())
	require.Equal(t, "job.snapshot", evs[0].Type)
	return evs[0]
}

func TestPersistedJobSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	yaml := regYAML(an("a", "ok"), an("b", "ok", deps("a")), finDef)
	h := map[string]adapter.Handler{"ok": ok, "final": ok}
	e1, st1 := sqliteEngine(t, path, yaml, Config{Finalizer: &fakeFinalizer{}}, h)

	_, sub := createWithEvents(t, e1, in("hello"))
	evs := drain(t, sub)
	id := evs[0].JobID
	lastSeq := evs[len(evs)-1].Seq
	waitDone(t, e1, id)
	for i := 0; i < 2; i++ { // later jobs: list order and cursors
		time.Sleep(2 * time.Millisecond)
		e1.Create(in("later"))
	}
	e1.Create(CreateRequest{Input: job.Input{Content: "x"}, Analyzers: []string{}})
	require.Eventually(t, func() bool {
		for _, j := range e1.List("") {
			if !j.Done {
				return false
			}
		}
		return true
	}, 10*time.Second, 5*time.Millisecond)

	wantJob, err := e1.Get(id)
	require.NoError(t, err)
	wantList := marshal(t, e1.List(""))
	wantSnap := marshal(t, snapshotOf(t, e1, id).Data)
	crash(t, e1, st1)

	e2, _ := sqliteEngine(t, path, yaml, Config{Finalizer: &fakeFinalizer{}}, h)
	n, err := e2.Recover()
	require.NoError(t, err)
	require.Zero(t, n)

	got, err := e2.Get(id)
	require.NoError(t, err)
	require.JSONEq(t, marshal(t, wantJob), marshal(t, got))
	require.Equal(t, job.StatusCompleted, got.Status)
	require.JSONEq(t, wantList, marshal(t, e2.List("")))
	require.Len(t, e2.List(job.StatusCompleted), 4)
	require.Empty(t, e2.List(job.StatusRunning))
	all := e2.List("")
	page, err := e2.Page("", "", "", &store.Cursor{CreatedAt: all[0].CreatedAt, ID: all[0].ID}, 1)
	require.NoError(t, err)
	require.Equal(t, []string{all[1].ID}, []string{page[0].ID}, "a cursor continues after its job")

	snap := snapshotOf(t, e2, id)
	require.Equal(t, lastSeq, snap.Seq, "seq of the snapshot is the last seq emitted before the restart")
	require.JSONEq(t, wantSnap, marshal(t, snap.Data))

	_, err = e2.Cancel(id)
	require.ErrorIs(t, err, ErrJobFinished)
	_, err = e2.Cancel("nope")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = e2.Subscribe("nope")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = e2.Get("nope")
	require.ErrorIs(t, err, ErrNotFound)
}

var hang = map[string]adapter.Handler{"ok": ok, "block": block, "final": ok}

// waitFor polls the job until pred holds.
func waitFor(t *testing.T, e *Engine, id string, pred func(job.Job) bool) {
	t.Helper()
	require.Eventually(t, func() bool {
		j, err := e.Get(id)
		return err == nil && pred(j)
	}, 10*time.Second, 2*time.Millisecond)
}

func TestRestartInterruptsRunningJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	yaml := regYAML(an("a", "ok"), an("hang", "block"), an("c", "ok", deps("hang")), an("off", "ok", "    enabled: false\n"), finDef)
	e1, st1 := sqliteEngine(t, path, yaml, Config{Finalizer: &fakeFinalizer{}}, hang)
	j, err := e1.Create(in("x"))
	require.NoError(t, err)
	waitFor(t, e1, j.ID, func(j job.Job) bool {
		return ar(j, "a").Status == job.RunSucceeded && ar(j, "hang").Status == job.RunRunning
	})
	live, _ := e1.Get(j.ID)
	sub, err := e1.Subscribe(j.ID)
	require.NoError(t, err)
	lastSeq := (<-sub.Events()).Seq
	sub.Close()
	crash(t, e1, st1)

	start := time.Now().UTC().Add(time.Hour) // the restart is after the stop
	e2, _ := sqliteEngine(t, path, yaml, Config{Finalizer: &fakeFinalizer{}, Now: func() time.Time { return start }}, hang)
	n, err := e2.Recover()
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err := e2.Get(j.ID)
	require.NoError(t, err)
	require.Equal(t, job.StatusFailed, got.Status)
	require.True(t, got.Done)
	require.Equal(t, start, *got.FinishedAt)
	require.Equal(t, ar(live, "a"), ar(got, "a"), "a settled run is unchanged")
	h := ar(got, "hang")
	require.Equal(t, job.RunFailed, h.Status)
	require.Equal(t, job.CodeInterrupted, h.Error.Code)
	require.Equal(t, start, *h.FinishedAt)
	require.Equal(t, start.Sub(*h.StartedAt).Milliseconds(), *h.DurationMS)
	require.Equal(t, job.AvailUnavailable, *h.Availability)
	c := ar(got, "c")
	require.Equal(t, job.RunSkipped, c.Status)
	require.Equal(t, job.ReasonInterrupted, *c.Reason)
	require.Nil(t, c.DurationMS)
	require.Equal(t, job.AvailUnavailable, *c.Availability)
	require.Equal(t, job.ReasonDisabled, *ar(got, "off").Reason, "creation-time skips stay")
	require.Equal(t, job.RunSkipped, got.Finalization.Status)
	require.Equal(t, job.ReasonInterrupted, *got.Finalization.Reason)
	require.Equal(t, job.ComputeCoverage(got.Analyzers), got.Coverage)
	require.Equal(t, got.Coverage.Total, got.Coverage.Settled)

	require.Empty(t, e2.List(job.StatusRunning))
	require.Len(t, e2.List(job.StatusFailed), 1)
	snap := snapshotOf(t, e2, j.ID)
	require.Equal(t, lastSeq, snap.Seq, "the interruption emits no event")
	d := snap.Data.(map[string]any)
	require.Equal(t, job.StatusFailed, d["job"].(job.Job).Status)
	require.Empty(t, d["activities"])
	_, err = e2.Cancel(j.ID)
	require.ErrorIs(t, err, ErrJobFinished)

	e2.Close() // a second start finds nothing more to do and ends nothing again
	e3, _ := sqliteEngine(t, path, yaml, Config{Now: func() time.Time { return start.Add(time.Hour) }}, hang)
	n, err = e3.Recover()
	require.NoError(t, err)
	require.Zero(t, n)
	again, _ := e3.Get(j.ID)
	require.Equal(t, start, *again.FinishedAt)
}

func TestRestartInterruptsFinalizingJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	yaml := regYAML(an("a", "ok"), finDef)
	fin := &fakeFinalizer{fn: func(ctx context.Context) (json.RawMessage, error) { <-ctx.Done(); return nil, ctx.Err() }}
	e1, st1 := sqliteEngine(t, path, yaml, Config{Finalizer: fin}, hang)
	j, _ := e1.Create(in("x"))
	waitFor(t, e1, j.ID, func(j job.Job) bool {
		return j.Status == job.StatusFinalizing && j.Finalization.Status == job.RunRunning
	})
	crash(t, e1, st1)

	e2, _ := sqliteEngine(t, path, yaml, Config{}, hang)
	_, err := e2.Recover()
	require.NoError(t, err)
	got, _ := e2.Get(j.ID)
	require.Equal(t, job.StatusFailed, got.Status)
	require.Equal(t, job.RunFailed, got.Finalization.Status)
	require.Equal(t, job.CodeInterrupted, got.Finalization.Error.Code)
	require.Nil(t, got.Finalization.Result)
	require.NotNil(t, got.Finalization.DurationMS)
	require.Equal(t, job.RunSucceeded, ar(got, "a").Status)
	require.JSONEq(t, `{"by":"a"}`, string(ar(got, "a").Result))
}

func TestRestartInterruptsScrapingJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	wait := moduleFunc(func(ctx context.Context, _ ss.Target, _ ss.Request) (*ss.Result, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	e1, st1 := sqliteEngine(t, path, scrapeRegistry, Config{Scraper: stubScraper(wait), Finalizer: &fakeFinalizer{}}, hang)
	j, err := e1.Create(scrapeReq("https://mastodon.social/@alice/1"))
	require.NoError(t, err)
	waitFor(t, e1, j.ID, func(j job.Job) bool { return j.Status == job.StatusScraping && j.Scrape.Status == job.RunRunning })
	crash(t, e1, st1)

	e2, _ := sqliteEngine(t, path, scrapeRegistry, Config{}, hang)
	_, err = e2.Recover()
	require.NoError(t, err)
	got, _ := e2.Get(j.ID)
	require.Equal(t, job.StatusFailed, got.Status)
	require.Equal(t, job.RunFailed, got.Scrape.Status)
	require.Equal(t, job.CodeInterrupted, got.Scrape.Error.Code)
	require.NotNil(t, got.Scrape.DurationMS)
	require.Nil(t, got.Input)
	require.Equal(t, job.ReasonInterrupted, *ar(got, "a").Reason)
	require.Equal(t, job.ReasonDisabled, *ar(got, "off").Reason)
	require.Equal(t, job.ReasonInterrupted, *got.Finalization.Reason)
}

func TestSweepKeepsPersistedHistoryUnlessHistoryRetentionSet(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "jobs.db")
	yaml := regYAML(an("a", "ok"))
	cfg := Config{JobRetention: time.Hour, Now: func() time.Time { return now }}
	e, _ := sqliteEngine(t, path, yaml, cfg, hang)
	j, _ := e.Create(in("x"))
	waitDone(t, e, j.ID)

	e.Sweep(now.Add(2 * time.Hour)) // beyond JOB_RETENTION: gone from memory, still in history
	_, inMemory := e.store.Get(j.ID)
	require.False(t, inMemory)
	_, err := e.Get(j.ID)
	require.NoError(t, err)
	require.Len(t, e.List(""), 1)
	snapshotOf(t, e, j.ID)
	_, err = e.Cancel(j.ID)
	require.ErrorIs(t, err, ErrJobFinished)

	cfg.HistoryRetention = 3 * time.Hour
	e2, _ := sqliteEngine(t, filepath.Join(t.TempDir(), "other.db"), yaml, cfg, hang)
	j2, _ := e2.Create(in("x"))
	waitDone(t, e2, j2.ID)
	e2.Sweep(now.Add(2 * time.Hour))
	_, err = e2.Get(j2.ID)
	require.NoError(t, err, "within the history retention")
	e2.Sweep(now.Add(4 * time.Hour))
	_, err = e2.Get(j2.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.Empty(t, e2.List(""))
	_, err = e2.Subscribe(j2.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestConcurrentReadsWhileJobsRun(t *testing.T) {
	yaml := regYAML(an("a", "ok"), an("b", "ok", deps("a")), an("c", "ok", deps("a")), finDef)
	e, _ := sqliteEngine(t, filepath.Join(t.TempDir(), "jobs.db"), yaml, Config{Finalizer: &fakeFinalizer{}}, hang)
	var wg sync.WaitGroup
	ids := make(chan string, 40)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				j, err := e.Create(in("x"))
				require.NoError(t, err)
				ids <- j.ID
				_, err = e.Get(j.ID)
				require.NoError(t, err)
				e.List("")
				if sub, err := e.Subscribe(j.ID); err == nil {
					sub.Close()
				}
			}
		}()
	}
	wg.Wait()
	close(ids)
	for id := range ids {
		got := waitDone(t, e, id)
		require.Equal(t, job.StatusCompleted, got.Status)
	}
	e.Sweep(time.Now().Add(100 * time.Hour)) // all evicted from memory: everything is read back from the database
	list, err := e.Page("", "", "", nil, 0)
	require.NoError(t, err)
	require.Len(t, list, 40)
	for _, j := range list {
		require.True(t, j.Done)
		require.Equal(t, job.RunSucceeded, ar(j, "c").Status)
		require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	}
}

func TestMemoryStoreIsTheDefaultAndHasNoHistory(t *testing.T) {
	e := newEngine(t, regYAML(an("a", "ok")), Config{}, hang)
	require.IsType(t, &store.Memory{}, e.store)
	n, err := e.Recover()
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestRestartKeepsPersistedLanguageConsistent(t *testing.T) {
	de := func(context.Context, adapter.Request) (any, error) {
		return map[string]any{"source_language": "DE"}, nil
	}
	h := map[string]adapter.Handler{"ok": ok, "block": block, "de": de}
	ids := func(e *Engine, lang string) []string {
		page, err := e.Page("", "", lang, nil, 0)
		require.NoError(t, err)
		out := []string{}
		for _, j := range page {
			out = append(out, j.ID)
		}
		return out
	}
	for name, c := range map[string]struct {
		yaml, lang string
	}{
		"translation interrupted": {regYAML(an("translation", "block")), store.LanguageUnknown},
		"translation succeeded":   {regYAML(an("translation", "de"), an("hang", "block", deps("translation"))), "de"},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "jobs.db")
			e1, st1 := sqliteEngine(t, path, c.yaml, Config{}, h)
			j, err := e1.Create(in("x"))
			require.NoError(t, err)
			waitFor(t, e1, j.ID, func(j job.Job) bool {
				return j.Status == job.StatusRunning && len(j.Analyzers) > 0 && ar(j, "translation").Status != job.RunPending
			})
			if c.lang == "de" {
				waitFor(t, e1, j.ID, func(j job.Job) bool { return ar(j, "translation").Status == job.RunSucceeded })
			}
			require.Equal(t, []string{j.ID}, ids(e1, c.lang), "before the restart")
			crash(t, e1, st1)

			e2, _ := sqliteEngine(t, path, c.yaml, Config{}, h)
			n, err := e2.Recover()
			require.NoError(t, err)
			require.Equal(t, 1, n)
			require.Equal(t, []string{j.ID}, ids(e2, c.lang), "after the restart")
		})
	}
}
