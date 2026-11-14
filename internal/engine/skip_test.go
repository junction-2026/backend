package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// skipExec skips analyzer "a" like an adapter would and runs the rest through the real executor.
type skipExec struct{ real Executor }

func (s skipExec) Execute(ctx context.Context, def *registry.Analyzer, req adapter.Request) (json.RawMessage, error) {
	if def.ID == "a" {
		return nil, &adapter.Skip{Reason: adapter.ReasonNoSearchableTopics}
	}
	return s.real.Execute(ctx, def, req)
}

func TestAdapterReportedSkip(t *testing.T) {
	e := newEngine(t, regYAML(an("a", "ok"), an("b", "ok", deps("a")), an("c", "ok")), Config{},
		map[string]adapter.Handler{"ok": ok})
	e.cfg.Executor = skipExec{e.cfg.Executor}
	j, sub := createWithEvents(t, e, in("x"))
	evs := drain(t, sub)
	fin := waitDone(t, e, j.ID)

	a := ar(fin, "a")
	require.Equal(t, job.RunSkipped, a.Status)
	require.Equal(t, "no_searchable_topics", *a.Reason)
	require.Nil(t, a.Result)
	require.Nil(t, a.Error)
	require.NotNil(t, a.StartedAt)
	require.Equal(t, job.ReasonDependencySkipped, *ar(fin, "b").Reason)
	require.Equal(t, job.RunSucceeded, ar(fin, "c").Status)
	require.Equal(t, job.StatusCompleted, fin.Status)
	ts := typesFor(evs)
	require.Less(t, index(ts, "analyzer.started:a"), index(ts, "analyzer.skipped:a"))
	require.Equal(t, 0, count(evs, "analyzer.failed"))
	requireSeq(t, evs)
}
