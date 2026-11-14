package job

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInterruptRunningJob(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	now := start.Add(90 * time.Second)
	done := start.Add(time.Second)
	r := &Record{Job: Job{
		ID: "j", Status: StatusRunning, StartedAt: &start,
		Analyzers: []AnalyzerRun{
			{ID: "techniques", Status: RunSucceeded, StartedAt: &start, FinishedAt: &done, Result: json.RawMessage(`{"x":1}`), Availability: Str(AvailRan)},
			{ID: "fact_check", Status: RunRunning, StartedAt: &start},
			{ID: "news_search", Status: RunPending, DependsOn: []string{"fact_check"}},
			{ID: "off", Status: RunSkipped, Reason: Str(ReasonNotSelected), Availability: Str(AvailNotApplicable), Note: Str("n")},
		},
		Finalization: Finalization{Status: RunPending},
	}, Plan: &Plan{Total: 4, Analyzers: []PlanEntry{{ID: "techniques"}, {ID: "fact_check"}, {ID: "news_search"}}},
		Progress:   &Progress{Done: 1, Running: []string{"fact_check"}, Total: 3},
		Activities: map[string]Activity{"fact_check": {Key: "fact_check"}}, LastSeq: 9}
	r.Coverage = ComputeCoverage(r.Analyzers)

	r.Interrupt(now)

	require.Equal(t, StatusFailed, r.Status)
	require.True(t, r.Done)
	require.Equal(t, &now, r.FinishedAt)
	a := r.Analyzers
	require.Equal(t, RunSucceeded, a[0].Status)
	require.JSONEq(t, `{"x":1}`, string(a[0].Result))
	require.Equal(t, &done, a[0].FinishedAt, "settled runs are unchanged")

	require.Equal(t, RunFailed, a[1].Status)
	require.Equal(t, CodeInterrupted, a[1].Error.Code)
	require.Equal(t, int64(90000), *a[1].DurationMS)
	require.Equal(t, &now, a[1].FinishedAt)
	require.Equal(t, AvailUnavailable, *a[1].Availability)

	require.Equal(t, RunSkipped, a[2].Status)
	require.Equal(t, ReasonInterrupted, *a[2].Reason)
	require.Nil(t, a[2].StartedAt)
	require.Nil(t, a[2].DurationMS)
	require.Equal(t, &now, a[2].FinishedAt)
	require.Equal(t, AvailUnavailable, *a[2].Availability)
	require.NotNil(t, a[2].Note)

	require.Equal(t, ReasonNotSelected, *a[3].Reason, "creation-time skips are unchanged")
	require.Equal(t, RunSkipped, r.Finalization.Status)
	require.Equal(t, ReasonInterrupted, *r.Finalization.Reason)
	require.Equal(t, ComputeCoverage(a), r.Coverage)
	require.Equal(t, 4, r.Coverage.Settled)

	require.Empty(t, r.Activities)
	require.Equal(t, &Progress{Done: 3, Running: []string{}, Total: 3}, r.Progress)
	require.EqualValues(t, 9, r.LastSeq)

	again := *r.Analyzers[1].FinishedAt
	r.Interrupt(now.Add(time.Hour)) // a done job is never touched again
	require.Equal(t, again, *r.Analyzers[1].FinishedAt)
}

func TestInterruptScrapingAndFinalizing(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	now := start.Add(2 * time.Second)

	s := &Record{Job: Job{Status: StatusScraping, Scrape: &Scrape{Status: RunRunning, StartedAt: &start},
		Analyzers: []AnalyzerRun{{ID: "a", Status: RunPending}}, Finalization: Finalization{Status: RunPending}}}
	s.Interrupt(now)
	require.Equal(t, RunFailed, s.Scrape.Status)
	require.Equal(t, CodeInterrupted, s.Scrape.Error.Code)
	require.Equal(t, int64(2000), *s.Scrape.DurationMS)
	require.Nil(t, s.Input)
	require.Equal(t, ReasonInterrupted, *s.Analyzers[0].Reason)

	f := &Record{Job: Job{Status: StatusFinalizing,
		Analyzers:    []AnalyzerRun{{ID: "a", Status: RunSucceeded, Result: json.RawMessage(`{"k":"v"}`)}},
		Finalization: Finalization{Status: RunRunning, StartedAt: &start}}}
	f.Interrupt(now)
	require.Equal(t, RunFailed, f.Finalization.Status)
	require.Equal(t, CodeInterrupted, f.Finalization.Error.Code)
	require.Nil(t, f.Finalization.Result)
	require.Equal(t, int64(2000), *f.Finalization.DurationMS)
	require.JSONEq(t, `{"k":"v"}`, string(f.Analyzers[0].Result))
}
