package engine

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
)

// fakeCountries implements CountryAssociator; fn decides per job id and call number (1-based).
type fakeCountries struct {
	mu    sync.Mutex
	calls map[string]int
	fn    func(ctx context.Context, id string, n int) ([]job.AssociatedCountry, error)
}

func (f *fakeCountries) Associate(ctx context.Context, j *job.Job) ([]job.AssociatedCountry, error) {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[j.ID]++
	n := f.calls[j.ID]
	f.mu.Unlock()
	return f.fn(ctx, j.ID, n)
}

func (f *fakeCountries) Calls(id string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls[id] }

var us = []job.AssociatedCountry{{CountryCode: "US", Country: "United States"}}

func detecting(names ...string) adapter.Handler {
	return func(context.Context, adapter.Request) (any, error) {
		d := []map[string]string{}
		for _, n := range names {
			d = append(d, map[string]string{"name": n, "display": n})
		}
		return map[string]any{"detected": d}, nil
	}
}

func TestCountryAssociationConcurrentWithFinalization(t *testing.T) {
	release := make(chan struct{})
	f := &fakeFinalizer{}
	ca := &fakeCountries{fn: func(ctx context.Context, _ string, _ int) ([]job.AssociatedCountry, error) {
		<-release
		return us, nil
	}}
	e := newEngine(t, regYAML(an("techniques", "tech"), finDef), Config{Finalizer: f, Countries: ca},
		map[string]adapter.Handler{"tech": detecting("Doubt"), "final": ok})
	j, sub := createWithEvents(t, e, in("US election text"))

	require.Eventually(t, func() bool { g, _ := e.Get(j.ID); return g.Finalization.Status == job.StatusCompleted }, 5*time.Second, time.Millisecond,
		"finalization settles while the association still runs")
	g, err := e.Get(j.ID)
	require.NoError(t, err)
	require.False(t, g.Done, "the job waits for the association")
	require.Nil(t, g.CountryAssociation, "null until settled")
	require.Equal(t, 1, ca.Calls(j.ID))
	close(release)

	evs := drain(t, sub)
	last := evs[len(evs)-1]
	require.Equal(t, "job.completed", last.Type)
	done := waitDone(t, e, j.ID)
	evJob := last.Data.(map[string]any)["job"].(job.Job)
	require.Equal(t, done.CountryAssociation, evJob.CountryAssociation)
	a := done.CountryAssociation
	require.Equal(t, job.RunSucceeded, a.Status)
	require.Nil(t, a.Reason)
	require.Nil(t, a.Error)
	require.NotNil(t, a.FinishedAt)
	require.Equal(t, us, a.Countries)
	require.Equal(t, 1, ca.Calls(j.ID), "runs once")
	require.Equal(t, 1, f.Calls())
	require.NotContains(t, types(evs), "country_association", "no new event type")
	b, err := json.Marshal(done)
	require.NoError(t, err)
	require.Contains(t, string(b), `"country_association":{"status":"succeeded","reason":null,"error":null,"countries":[{"country_code":"US","country":"United States"}],"finished_at":`)
}

func TestCountryAssociationSkipped(t *testing.T) {
	ca := &fakeCountries{fn: func(context.Context, string, int) ([]job.AssociatedCountry, error) { return us, nil }}
	e := newEngine(t, regYAML(an("techniques", "tech")), Config{Countries: ca}, map[string]adapter.Handler{"tech": detecting()})
	j, err := e.Create(in("x"))
	require.NoError(t, err)
	a := waitDone(t, e, j.ID).CountryAssociation
	require.Equal(t, &job.CountryAssociation{Status: job.RunSkipped, Reason: job.Str(job.ReasonNoDetectedPropaganda), Countries: []job.AssociatedCountry{}, FinishedAt: a.FinishedAt}, a)
	require.Equal(t, 0, ca.Calls(j.ID), "no model call")

	e = newEngine(t, regYAML(an("techniques", "tech")), Config{}, map[string]adapter.Handler{"tech": detecting("Doubt")})
	j, err = e.Create(in("x"))
	require.NoError(t, err)
	done := waitDone(t, e, j.ID)
	require.Equal(t, job.StatusCompleted, done.Status)
	require.Equal(t, job.RunSkipped, done.CountryAssociation.Status)
	require.Equal(t, job.ReasonNotConfigured, *done.CountryAssociation.Reason)
	require.Nil(t, done.CountryAssociation.Error)
}

func TestCountryAssociationFailureIsolated(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{&job.RunError{Code: job.CodeAdapterError, Message: "llm provider HTTP 500"}, job.CodeAdapterError},
		{&job.RunError{Code: job.CodeInvalidOutput, Message: "reply has no countries array"}, job.CodeInvalidOutput},
		{&job.RunError{Code: job.CodeTimeout, Message: "country association did not answer within its timeout"}, job.CodeTimeout},
		{errors.New("boom"), job.CodeAdapterError},
	} {
		t.Run(tc.code, func(t *testing.T) {
			ca := &fakeCountries{fn: func(context.Context, string, int) ([]job.AssociatedCountry, error) { return nil, tc.err }}
			f := &fakeFinalizer{}
			e := newEngine(t, regYAML(an("techniques", "tech"), finDef), Config{Finalizer: f, Countries: ca},
				map[string]adapter.Handler{"tech": detecting("Doubt"), "final": ok})
			j, err := e.Create(in("x"))
			require.NoError(t, err)
			done := waitDone(t, e, j.ID)
			require.Equal(t, job.StatusCompleted, done.Status)
			require.Equal(t, job.StatusCompleted, done.Finalization.Status)
			require.NotNil(t, done.Risk)
			require.Equal(t, job.RunSucceeded, ar(done, "techniques").Status)
			a := done.CountryAssociation
			require.Equal(t, job.RunFailed, a.Status)
			require.Equal(t, tc.code, a.Error.Code)
			require.Equal(t, []job.AssociatedCountry{}, a.Countries)
			require.Nil(t, a.Reason)
		})
	}
}

func TestCountryAssociationCancelled(t *testing.T) {
	started := make(chan struct{})
	ca := &fakeCountries{fn: func(ctx context.Context, _ string, _ int) ([]job.AssociatedCountry, error) {
		close(started)
		<-ctx.Done()
		return nil, adapter.ContextError(ctx)
	}}
	e := newEngine(t, regYAML(an("techniques", "tech")), Config{Countries: ca}, map[string]adapter.Handler{"tech": detecting("Doubt")})
	j, sub := createWithEvents(t, e, in("x"))
	<-started
	c, err := e.Cancel(j.ID)
	require.NoError(t, err)
	evs := drain(t, sub)
	last := evs[len(evs)-1]
	require.Equal(t, "job.cancelled", last.Type)
	a := last.Data.(map[string]any)["job"].(job.Job).CountryAssociation
	require.Equal(t, job.RunFailed, a.Status)
	require.Equal(t, job.CodeCancelled, a.Error.Code)
	require.Equal(t, a, c.CountryAssociation)
}

func TestCountryAssociationNotRunWhenCancelledBefore(t *testing.T) {
	ca := &fakeCountries{fn: func(context.Context, string, int) ([]job.AssociatedCountry, error) { return us, nil }}
	e := newEngine(t, regYAML(an("techniques", "blk")), Config{Countries: ca}, map[string]adapter.Handler{"blk": block})
	j, err := e.Create(in("x"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { g, _ := e.Get(j.ID); return ar(g, "techniques").Status == job.RunRunning }, 5*time.Second, time.Millisecond)
	c, err := e.Cancel(j.ID)
	require.NoError(t, err)
	require.Nil(t, c.CountryAssociation)
	require.Equal(t, 0, ca.Calls(j.ID))
}

// a job whose step was running when the server stopped gets failed / interrupted at the next start.
func TestCountryAssociationInterruptedByRestart(t *testing.T) {
	ca := &fakeCountries{fn: func(ctx context.Context, _ string, _ int) ([]job.AssociatedCountry, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	path := filepath.Join(t.TempDir(), "jobs.db")
	yaml := regYAML(an("techniques", "tech"))
	h := map[string]adapter.Handler{"tech": detecting("Doubt")}
	e, st := sqliteEngine(t, path, yaml, Config{Countries: ca}, h)
	j, err := e.Create(in("x"))
	require.NoError(t, err)
	// the step starts inside the barrier's locked section; Get sees risk only after that section persisted the job
	require.Eventually(t, func() bool { g, _ := e.Get(j.ID); return ca.Calls(j.ID) == 1 && g.Risk != nil }, 5*time.Second, time.Millisecond)
	crash(t, e, st)

	e2, _ := sqliteEngine(t, path, yaml, Config{}, h)
	_, err = e2.Recover()
	require.NoError(t, err)
	got, err := e2.Get(j.ID)
	require.NoError(t, err)
	require.Equal(t, job.StatusFailed, got.Status)
	require.Equal(t, job.RunFailed, got.CountryAssociation.Status)
	require.Equal(t, job.CodeInterrupted, got.CountryAssociation.Error.Code)
}

// stored done jobs without an association are backfilled in batches; failed attempts are retried up to 3 times;
// non-propaganda, non-public and settled jobs are left alone; the map gets the new countries.
func TestCountryAssociationBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	_, st := sqliteEngine(t, path, regYAML(an("a", "ok")), Config{}, map[string]adapter.Handler{"ok": ok})
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	stored := func(id string, public bool, detected string, a *job.CountryAssociation) {
		r := &job.Record{Job: job.Job{ID: id, Status: job.StatusCompleted, Done: true, Public: public, CreatedAt: at, FinishedAt: &at,
			Input:        &job.Input{Content: "US politics", ContentType: "text/plain"},
			Analyzers:    []job.AnalyzerRun{{ID: "techniques", Status: job.RunSucceeded, Result: json.RawMessage(`{"detected":[` + detected + `]}`)}},
			Finalization: job.Finalization{Status: job.RunSkipped}, CountryAssociation: a}, Previews: map[string]any{}}
		at = at.Add(time.Second)
		require.NoError(t, st.Save(r))
	}
	doubt := `{"name":"Doubt","display":"Doubt"}`
	ok1 := job.NewCountryAssociation(at, []job.AssociatedCountry{{CountryCode: "FR", Country: "France"}}, nil, nil)
	stored("pasted", true, doubt, nil)
	stored("retry", true, doubt, job.NewCountryAssociation(at, nil, nil, &job.RunError{Code: job.CodeTimeout, Message: "t"}))
	stored("always", true, doubt, nil)
	stored("noprop", true, "", nil)
	stored("unknown", true, `{"name":"Made_Up"}`, nil)
	stored("private", false, doubt, nil)
	stored("settled", true, doubt, ok1)
	stored("skipped", true, doubt, job.NewCountryAssociation(at, nil, job.Str(job.ReasonNotConfigured), nil))
	for i := 0; i < 4; i++ { // more than one batch
		stored("bulk"+string(rune('a'+i)), true, doubt, nil)
	}
	require.NoError(t, st.Close())

	ca := &fakeCountries{fn: func(_ context.Context, id string, n int) ([]job.AssociatedCountry, error) {
		switch {
		case id == "always" || (id == "retry" && n == 1):
			return nil, &job.RunError{Code: job.CodeAdapterError, Message: "llm provider HTTP 500"}
		}
		return us, nil
	}}
	e, _ := sqliteEngine(t, path, regYAML(an("a", "ok")), Config{Countries: ca, CountryBackfillBatch: 3, CountryBackfillPause: time.Millisecond},
		map[string]adapter.Handler{"ok": ok})
	_, err := e.Recover()
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return ca.Calls("always") == countryMaxAttempts && ca.Calls("bulkd") == 1 && ca.Calls("retry") == 2
	},
		10*time.Second, time.Millisecond)
	e.Close() // waits for the backfill
	got := func(id string) job.Job {
		j, err := e.Get(id)
		require.NoError(t, err)
		return j
	}

	require.Equal(t, us, got("pasted").CountryAssociation.Countries)
	require.Equal(t, job.RunSucceeded, got("retry").CountryAssociation.Status)
	require.Equal(t, job.RunFailed, got("always").CountryAssociation.Status)
	require.Equal(t, countryMaxAttempts, ca.Calls("always"), "bounded attempts")
	for _, id := range []string{"noprop", "unknown", "private", "settled", "skipped"} {
		require.Equal(t, 0, ca.Calls(id), id)
	}
	require.Nil(t, got("noprop").CountryAssociation)
	require.Nil(t, got("private").CountryAssociation)
	require.Equal(t, ok1, got("settled").CountryAssociation)
	require.Equal(t, job.RunSkipped, got("skipped").CountryAssociation.Status)

	countries, total, err := e.MapCountries()
	require.NoError(t, err)
	require.Len(t, countries, 2)
	require.Equal(t, "FR", countries[0].Code)
	require.Equal(t, "US", countries[1].Code)
	require.Equal(t, 6, countries[1].Analyses, "pasted, retry and the 4 bulk jobs")
	require.Equal(t, 7, total)
	items, err := e.MapAnalyses("US", nil, 0)
	require.NoError(t, err)
	require.Len(t, items, 6)
	require.Nil(t, items[0].Values.Outlet)
	require.Equal(t, []string{"content"}, items[0].Values.Countries[0].Basis)
}
