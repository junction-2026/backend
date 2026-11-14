package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
)

func TestDAGOrderAndConcurrentIndependents(t *testing.T) {
	var mu sync.Mutex
	var order []string
	rec := func(ctx context.Context, req adapter.Request) (any, error) {
		mu.Lock()
		order = append(order, req.AnalyzerID)
		mu.Unlock()
		return ok(ctx, req)
	}
	e := newEngine(t, regYAML(an("a", "rec"), an("b", "rec", deps("a")), an("c", "rec", deps("b")), an("d", "rec")),
		Config{}, map[string]adapter.Handler{"rec": rec})
	j, sub := createWithEvents(t, e, in("hello"))
	evs := drain(t, sub)
	requireSeq(t, evs)
	fin := waitDone(t, e, j.ID)

	require.Equal(t, job.StatusCompleted, fin.Status)
	require.True(t, fin.Done)
	for _, r := range fin.Analyzers {
		require.Equal(t, job.RunSucceeded, r.Status, r.ID)
		require.Contains(t, string(r.Result), r.ID)
	}
	require.Less(t, index(order, "a"), index(order, "b"))
	require.Less(t, index(order, "b"), index(order, "c"))

	got := typesFor(evs)
	require.Equal(t, "job.started", got[1])
	require.Less(t, index(got, "analyzer.started:b"), index(got, "analyzer.succeeded:b"))
	require.Less(t, index(got, "analyzer.succeeded:a"), index(got, "analyzer.started:b"))
	require.Equal(t, []string{"analysis.ready_for_finalization", "job.completed"}, got[len(got)-2:])
	require.Equal(t, 4, fin.Coverage.Succeeded)
	require.Equal(t, job.Finalization{Status: job.RunSkipped, Reason: job.Str(job.ReasonNotConfigured)}, fin.Finalization)
}

func TestParallelLimitPerJob(t *testing.T) {
	var cur, peak atomic.Int32
	slow := func(ctx context.Context, req adapter.Request) (any, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		cur.Add(-1)
		return ok(ctx, req)
	}
	var ys []string
	for i := 0; i < 6; i++ {
		ys = append(ys, an(fmt.Sprintf("a%d", i), "slow"))
	}
	e := newEngine(t, regYAML(ys...), Config{MaxParallelAnalyzers: 2}, map[string]adapter.Handler{"slow": slow})
	j, err := e.Create(in("x"))
	require.NoError(t, err)
	waitDone(t, e, j.ID)
	require.EqualValues(t, 2, peak.Load())
}

func TestGlobalJobLimitQueues(t *testing.T) {
	release := make(chan struct{})
	held := func(ctx context.Context, req adapter.Request) (any, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ok(ctx, req)
	}
	e := newEngine(t, regYAML(an("a", "held")), Config{MaxParallelJobs: 1}, map[string]adapter.Handler{"held": held})
	j1, _ := e.Create(in("1"))
	j2, _ := e.Create(in("2"))
	j3, _ := e.Create(in("3"))
	require.Eventually(t, func() bool { j, _ := e.Get(j1.ID); return j.Status == job.StatusRunning }, 5*time.Second, time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	q, _ := e.Get(j2.ID)
	require.Equal(t, job.StatusQueued, q.Status)
	require.Nil(t, q.StartedAt)

	// a queued job can be cancelled; its stream then shows the full lifecycle
	sub, _ := e.Subscribe(j2.ID)
	c, err := e.Cancel(j2.ID)
	require.NoError(t, err)
	require.Equal(t, job.StatusCancelled, c.Status)
	require.Equal(t, job.ReasonCancelled, *c.Analyzers[0].Reason)
	require.Equal(t, []string{"job.snapshot", "job.started", "analyzer.skipped", "job.cancelled"}, types(drain(t, sub)))

	// the slot goes to the next job in creation order, skipping the cancelled one
	close(release)
	waitDone(t, e, j1.ID)
	require.Equal(t, job.StatusCompleted, waitDone(t, e, j3.ID).Status)
	e.mu.Lock()
	defer e.mu.Unlock()
	require.Zero(t, e.running)
	require.Empty(t, e.waiting)
}

func TestFailureAndSkipPropagation(t *testing.T) {
	boom := func(context.Context, adapter.Request) (any, error) { return nil, errors.New("boom") }
	e := newEngine(t, regYAML(an("a", "boom"), an("b", "ok", deps("a")), an("c", "ok", deps("b")), an("d", "ok"),
		an("e", "ok", deps("a", "d"))), Config{}, map[string]adapter.Handler{"boom": boom, "ok": ok})
	j, sub := createWithEvents(t, e, in("x"))
	evs := drain(t, sub)
	fin := waitDone(t, e, j.ID)

	a := ar(fin, "a")
	require.Equal(t, job.RunFailed, a.Status)
	require.Equal(t, job.CodeAdapterError, a.Error.Code)
	require.Nil(t, a.Result)
	require.Equal(t, job.ReasonDependencyFailed, *ar(fin, "b").Reason)
	require.Equal(t, job.ReasonDependencySkipped, *ar(fin, "c").Reason)
	require.Equal(t, job.ReasonDependencyFailed, *ar(fin, "e").Reason) // failed wins over succeeded
	require.Equal(t, job.RunSucceeded, ar(fin, "d").Status)
	require.Equal(t, job.StatusCompleted, fin.Status, "analyzer failures do not fail the job")
	// exactly one settle event per analyzer
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		n := 0
		for _, s := range typesFor(evs) {
			if strings.HasSuffix(s, ":"+id) && !strings.HasPrefix(s, "analyzer.started") {
				n++
			}
		}
		require.Equal(t, 1, n, id)
	}
}

func TestTimeout(t *testing.T) {
	e := newEngine(t, regYAML("  - id: slow\n    execution: {adapter: custom, handler: block, timeout: 50ms}\n", an("next", "ok", deps("slow"))),
		Config{}, map[string]adapter.Handler{"block": block, "ok": ok})
	j, _ := e.Create(in("x"))
	fin := waitDone(t, e, j.ID)
	s := ar(fin, "slow")
	require.Equal(t, job.RunFailed, s.Status)
	require.Equal(t, job.CodeTimeout, s.Error.Code)
	require.Equal(t, job.ReasonDependencyFailed, *ar(fin, "next").Reason)
}

func TestSelectionApplicabilityAndCreationSkips(t *testing.T) {
	y := regYAML(
		an("entities", "ok"),
		an("claims", "ok", deps("entities")),
		an("other", "ok"),
		"  - id: legacy\n    enabled: false\n    execution: {adapter: custom, handler: ok}\n",
		"  - id: htmlonly\n    applies_to: {content_types: [text/html]}\n    execution: {adapter: custom, handler: ok}\n",
		"  - id: needsurl\n    applies_to: {requires_source_url: true}\n    execution: {adapter: custom, handler: ok}\n",
		"  - id: long\n    applies_to: {min_content_length: 50}\n    execution: {adapter: custom, handler: ok}\n",
		finDef)
	e := newEngine(t, y, Config{Finalizer: &fakeFinalizer{}}, map[string]adapter.Handler{"ok": ok, "final": ok})

	// all selected: creation skips with reasons; finalizer is not an AnalyzerRun
	j, err := e.Create(in("short text"))
	require.NoError(t, err)
	reasons := map[string]string{}
	for _, r := range j.Analyzers {
		if r.Reason != nil {
			reasons[r.ID] = *r.Reason
		}
	}
	require.Equal(t, map[string]string{"legacy": "disabled", "htmlonly": "not_applicable", "needsurl": "not_applicable", "long": "not_applicable"}, reasons)
	require.Len(t, j.Analyzers, 7)
	fin := waitDone(t, e, j.ID)
	require.Equal(t, job.StatusCompleted, fin.Status, "creation skips never block the barrier")
	require.Equal(t, "final_synthesis", *fin.Finalization.AnalyzerID)

	// selecting claims pulls in entities; others not_selected
	sel := []string{"claims"}
	j, err = e.Create(CreateRequest{Input: job.Input{Content: "<p>hi</p>", ContentType: "text/html", Source: job.Source{URL: "http://x"}}, Analyzers: sel})
	require.NoError(t, err)
	fin = waitDone(t, e, j.ID)
	require.Equal(t, job.RunSucceeded, ar(fin, "entities").Status)
	require.Equal(t, job.RunSucceeded, ar(fin, "claims").Status)
	require.Equal(t, job.ReasonNotSelected, *ar(fin, "other").Reason)
	require.Equal(t, job.ReasonNotSelected, *ar(fin, "htmlonly").Reason)
	require.Equal(t, job.ReasonDisabled, *ar(fin, "legacy").Reason)

	// empty selection selects nothing and completes
	j, err = e.Create(CreateRequest{Input: job.Input{Content: "x"}, Analyzers: []string{}})
	require.NoError(t, err)
	require.Equal(t, job.StatusCompleted, waitDone(t, e, j.ID).Status)

	// typed errors
	_, err = e.Create(CreateRequest{Input: job.Input{Content: "x"}, Analyzers: []string{"nope", "claims", "nope"}})
	var ue *UnknownAnalyzerError
	require.ErrorAs(t, err, &ue)
	require.Equal(t, []string{"nope"}, ue.IDs)
	_, err = e.Create(CreateRequest{Input: job.Input{Content: "x"}, Analyzers: []string{"final_synthesis"}})
	var ne *NotSelectableError
	require.ErrorAs(t, err, &ne)
	require.Equal(t, []string{"final_synthesis"}, ne.IDs)
	require.False(t, errors.As(err, &ue))
}

func TestInputDefaultsAndTextStatsBuiltin(t *testing.T) {
	e := newEngine(t, regYAML("  - id: stats\n    execution: {adapter: custom, handler: text_stats}\n"), Config{}, nil)
	j, err := e.Create(CreateRequest{Input: job.Input{Content: "<p>Hello   <b>wor</b>ld</p><p>second line</p>", ContentType: "text/html"}})
	require.NoError(t, err)
	fin := waitDone(t, e, j.ID)
	require.JSONEq(t, `{"characters":25,"words":4,"lines":2}`, string(ar(fin, "stats").Result))
	// defaults applied
	j, _ = e.Create(in("plain"))
	require.Equal(t, "text/plain", j.Input.ContentType)
}

func TestOutputSchemaViolation(t *testing.T) {
	y := "analyzers:\n  - id: scored\n    execution: {adapter: custom, handler: bad}\n    output:\n      schema:\n        type: object\n        required: [score]\n        properties: {score: {type: number}}\n"
	bad := func(context.Context, adapter.Request) (any, error) { return map[string]any{"score": "high"}, nil }
	e := newEngine(t, y, Config{}, map[string]adapter.Handler{"bad": bad})
	j, _ := e.Create(in("x"))
	r := ar(waitDone(t, e, j.ID), "scored")
	require.Equal(t, job.RunFailed, r.Status)
	require.Equal(t, job.CodeInvalidOutput, r.Error.Code)
	require.Contains(t, r.Error.Message, "/score")
	require.Nil(t, r.Result)
}

func TestFinalizerLifecycle(t *testing.T) {
	f := &fakeFinalizer{}
	e := newEngine(t, regYAML(an("a", "ok"), an("b", "ok"), finDef), Config{Finalizer: f}, map[string]adapter.Handler{"ok": ok, "final": ok})
	j, sub := createWithEvents(t, e, in("x"))
	require.Equal(t, job.RunPending, j.Finalization.Status)
	evs := drain(t, sub)
	requireSeq(t, evs)
	got := types(evs)
	require.Equal(t, []string{"analysis.ready_for_finalization", "analysis.finalizing", "analysis.finalized", "job.completed"}, got[len(got)-4:])
	fin := waitDone(t, e, j.ID)
	require.Equal(t, job.StatusCompleted, fin.Finalization.Status)
	require.JSONEq(t, `{"verdict":"low_risk","categories":[],"explanation":"ok"}`, string(fin.Finalization.Result))
	require.Equal(t, 1, f.Calls())
	require.Equal(t, 2, f.in.Job.Coverage.Settled, "finalizer sees all analyzers settled")
	require.Equal(t, "final_synthesis", f.in.Analyzer.ID)
	// event data equals REST state
	last := evs[len(evs)-1].Data.(map[string]any)["job"].(job.Job)
	require.Equal(t, fin.Finalization, last.Finalization)
}

func TestFinalizerFailuresKeepResults(t *testing.T) {
	cases := map[string]struct {
		fn   func(ctx context.Context) ([]byte, error)
		code string
	}{
		"error": {func(context.Context) ([]byte, error) { return nil, errors.New("down") }, job.CodeAdapterError},
		"typed": {func(context.Context) ([]byte, error) {
			return nil, &job.RunError{Code: job.CodeInvalidOutput, Message: "bad"}
		}, job.CodeInvalidOutput},
		"timeout": {func(ctx context.Context) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }, job.CodeTimeout},
		"array":   {func(context.Context) ([]byte, error) { return []byte(`[1]`), nil }, job.CodeInvalidOutput},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeFinalizer{fn: func(ctx context.Context) (rawMsg, error) { return c.fn(ctx) }}
			y := strings.Replace(finDef, "timeout: 1s", "timeout: 50ms", 1)
			e := newEngine(t, regYAML(an("a", "ok"), y), Config{Finalizer: f}, map[string]adapter.Handler{"ok": ok, "final": ok})
			j, sub := createWithEvents(t, e, in("x"))
			got := types(drain(t, sub))
			require.Equal(t, []string{"analysis.finalizing", "analysis.finalization_failed", "job.completed"}, got[len(got)-3:])
			fin := waitDone(t, e, j.ID)
			require.Equal(t, job.StatusCompleted, fin.Status)
			require.Equal(t, job.RunFailed, fin.Finalization.Status)
			require.Equal(t, c.code, fin.Finalization.Error.Code)
			require.Equal(t, job.RunSucceeded, ar(fin, "a").Status)
			require.NotNil(t, ar(fin, "a").Result)
		})
	}
}

func TestFinalizationSkipVariants(t *testing.T) {
	f := &fakeFinalizer{}
	h := map[string]adapter.Handler{"ok": ok, "final": ok}

	// finalize=false
	e := newEngine(t, regYAML(an("a", "ok"), finDef), Config{Finalizer: f}, h)
	off := false
	j, sub := createWithEvents(t, e, CreateRequest{Input: job.Input{Content: "x"}, Finalize: &off})
	require.Equal(t, job.ReasonDisabledByRequest, *j.Finalization.Reason)
	got := types(drain(t, sub))
	require.Equal(t, []string{"analysis.ready_for_finalization", "job.completed"}, got[len(got)-2:])
	require.Equal(t, job.StatusCompleted, waitDone(t, e, j.ID).Status)

	// finalizer in the registry but no Finalizer registered
	e = newEngine(t, regYAML(an("a", "ok"), finDef), Config{}, h)
	j, _ = e.Create(in("x"))
	require.Equal(t, job.ReasonNotConfigured, *j.Finalization.Reason)

	// Finalizer registered but no enabled finalizer in the registry
	e = newEngine(t, regYAML(an("a", "ok")), Config{Finalizer: f}, h)
	j, _ = e.Create(in("x"))
	require.Equal(t, job.ReasonNotConfigured, *j.Finalization.Reason)
	require.Nil(t, j.Finalization.AnalyzerID)
	waitDone(t, e, j.ID)
	require.Zero(t, f.Calls())
}

func TestCancelRunningJob(t *testing.T) {
	started := make(chan struct{}, 1)
	blk := func(ctx context.Context, _ adapter.Request) (any, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	e := newEngine(t, regYAML(an("done", "ok"), an("hang", "blk"), an("after", "ok", deps("hang"))),
		Config{}, map[string]adapter.Handler{"ok": ok, "blk": blk})
	j, sub := createWithEvents(t, e, in("x"))
	<-started
	require.Eventually(t, func() bool { j, _ := e.Get(j.ID); return ar(j, "done").Status == job.RunSucceeded }, 5*time.Second, time.Millisecond)

	c, err := e.Cancel(j.ID)
	require.NoError(t, err)
	require.Equal(t, job.StatusCancelled, c.Status)
	require.True(t, c.Done)
	require.Equal(t, job.RunSucceeded, ar(c, "done").Status, "settled results are kept")
	require.NotNil(t, ar(c, "done").Result)
	require.Equal(t, job.CodeCancelled, ar(c, "hang").Error.Code)
	require.Equal(t, job.ReasonCancelled, *ar(c, "after").Reason)

	evs := drain(t, sub)
	requireSeq(t, evs)
	got := typesFor(evs)
	require.Equal(t, "job.cancelled", got[len(got)-1])
	require.Equal(t, 0, count(evs, "analysis.ready_for_finalization")+count(evs, "job.completed"))
	require.Less(t, index(got, "analyzer.failed:hang"), index(got, "job.cancelled"))
	require.Less(t, index(got, "analyzer.skipped:after"), index(got, "job.cancelled"))

	_, err = e.Cancel(j.ID)
	require.ErrorIs(t, err, ErrJobFinished)
	_, err = e.Cancel("nope")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestCancelDuringFinalization(t *testing.T) {
	entered := make(chan struct{})
	f := &fakeFinalizer{fn: func(ctx context.Context) (rawMsg, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	e := newEngine(t, regYAML(an("a", "ok"), finDef), Config{Finalizer: f}, map[string]adapter.Handler{"ok": ok, "final": ok})
	j, sub := createWithEvents(t, e, in("x"))
	<-entered
	c, err := e.Cancel(j.ID)
	require.NoError(t, err)
	require.Equal(t, job.RunFailed, c.Finalization.Status)
	require.Equal(t, job.CodeCancelled, c.Finalization.Error.Code)
	evs := drain(t, sub)
	got := types(evs)
	require.Equal(t, []string{"analysis.finalizing", "analysis.finalization_failed", "job.cancelled"}, got[len(got)-3:])
	require.Zero(t, count(evs, "job.completed"))
	time.Sleep(20 * time.Millisecond) // the aborted finalizer returns; nothing may change
	again, _ := e.Get(j.ID)
	require.Equal(t, c.Finalization, again.Finalization)
}

func TestCancelPendingFinalizationAndSkippedStays(t *testing.T) {
	e := newEngine(t, regYAML(an("hang", "block"), finDef), Config{Finalizer: &fakeFinalizer{}}, map[string]adapter.Handler{"block": block, "final": ok})
	j, _ := e.Create(in("x"))
	c, err := e.Cancel(j.ID)
	require.NoError(t, err)
	require.Equal(t, job.RunSkipped, c.Finalization.Status)
	require.Equal(t, job.ReasonCancelled, *c.Finalization.Reason)

	e = newEngine(t, regYAML(an("hang", "block")), Config{}, map[string]adapter.Handler{"block": block})
	j, _ = e.Create(in("x"))
	c, _ = e.Cancel(j.ID)
	require.Equal(t, job.ReasonNotConfigured, *c.Finalization.Reason)
}

func TestSubscribeSnapshotAndSlowConsumer(t *testing.T) {
	var ys []string
	for i := 0; i < 5; i++ {
		ys = append(ys, an(fmt.Sprintf("a%d", i), "ok"))
	}
	e := newEngine(t, regYAML(ys...), Config{EventBuffer: 2}, map[string]adapter.Handler{"ok": ok})
	e.gate = make(chan struct{})
	j, _ := e.Create(in("x"))
	slow, _ := e.Subscribe(j.ID) // never read until the job is over
	close(e.gate)
	waitDone(t, e, j.ID)
	n := 0
	for range slow.Events() {
		n++
	}
	require.ErrorIs(t, slow.Err(), eventsErrSlow)
	require.LessOrEqual(t, n, 3)

	// a finished job yields exactly the snapshot, with the job's last seq
	late, err := e.Subscribe(j.ID)
	require.NoError(t, err)
	evs := drain(t, late)
	require.Len(t, evs, 1)
	require.Equal(t, "job.snapshot", evs[0].Type)
	require.EqualValues(t, 13, evs[0].Seq) // job.started, 5x(started, succeeded), ready, completed
	require.True(t, evs[0].Data.(map[string]any)["job"].(job.Job).Done)
	require.Nil(t, late.Err())
	_, err = e.Subscribe("nope")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestLateSubscriberGetsSnapshotThenRest(t *testing.T) {
	gate := make(chan struct{})
	g := func(ctx context.Context, req adapter.Request) (any, error) { <-gate; return ok(ctx, req) }
	e := newEngine(t, regYAML(an("fast", "ok"), an("slow", "g")), Config{}, map[string]adapter.Handler{"ok": ok, "g": g})
	j, _ := e.Create(in("x"))
	require.Eventually(t, func() bool { j, _ := e.Get(j.ID); return ar(j, "fast").Status == job.RunSucceeded }, 5*time.Second, time.Millisecond)
	sub, _ := e.Subscribe(j.ID)
	snap := <-sub.Events()
	sj := snap.Data.(map[string]any)["job"].(job.Job)
	require.Equal(t, job.RunSucceeded, ar(sj, "fast").Status)
	close(gate)
	rest := drain(t, sub)
	require.Equal(t, snap.Seq+1, rest[0].Seq)
	require.Equal(t, "job.completed", rest[len(rest)-1].Type)
}

func TestEvictionKeepsRunningJobs(t *testing.T) {
	now := time.Now()
	e := newEngine(t, regYAML(an("a", "ok"), an("hang", "block")), Config{JobRetention: time.Hour, Now: func() time.Time { return now }},
		map[string]adapter.Handler{"ok": ok, "block": block})
	running, _ := e.Create(in("x"))
	done, _ := e.Create(CreateRequest{Input: job.Input{Content: "x"}, Analyzers: []string{"a"}})
	waitDone(t, e, done.ID)

	e.Sweep(now.Add(59 * time.Minute))
	_, err := e.Get(done.ID)
	require.NoError(t, err)
	e.Sweep(now.Add(2 * time.Hour))
	_, err = e.Get(done.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = e.Subscribe(done.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = e.Get(running.ID)
	require.NoError(t, err, "running jobs are never evicted")
	require.Len(t, e.List(""), 1)
	require.Len(t, e.List("running"), 1)
	require.Len(t, e.List("completed"), 0)
}

func TestListOrder(t *testing.T) {
	e := newEngine(t, regYAML(an("a", "ok")), Config{}, map[string]adapter.Handler{"ok": ok})
	for i := 0; i < 3; i++ {
		e.Create(in("x"))
		time.Sleep(2 * time.Millisecond)
	}
	l := e.List("")
	require.Len(t, l, 3)
	require.True(t, l[0].CreatedAt.After(l[1].CreatedAt) && l[1].CreatedAt.After(l[2].CreatedAt))
}

// The barrier must fire exactly once when many analyzers settle at once.
func TestBarrierExactlyOnceUnderConcurrentSettles(t *testing.T) {
	const analyzers = 50
	var ys []string
	for i := 0; i < analyzers; i++ {
		ys = append(ys, an(fmt.Sprintf("a%02d", i), "gate"))
	}
	ys = append(ys, finDef)
	for iter := 0; iter < 100; iter++ {
		gate := make(chan struct{})
		var ready atomic.Int32
		g := func(ctx context.Context, req adapter.Request) (any, error) {
			ready.Add(1)
			<-gate
			return ok(ctx, req)
		}
		f := &fakeFinalizer{}
		e := newEngine(t, regYAML(ys...), Config{MaxParallelAnalyzers: analyzers, Finalizer: f}, map[string]adapter.Handler{"gate": g, "final": ok})
		j, sub := createWithEvents(t, e, in("x"))
		for ready.Load() < analyzers {
			time.Sleep(100 * time.Microsecond)
		}
		close(gate)
		evs := drain(t, sub)
		requireSeq(t, evs)
		require.Equal(t, 1, count(evs, "analysis.ready_for_finalization"), "iter %d", iter)
		require.Equal(t, 1, count(evs, "analysis.finalizing"))
		require.Equal(t, 1, count(evs, "job.completed"))
		require.Equal(t, 1, f.Calls())
		// ready_for_finalization follows the last settle event
		got := types(evs)
		ri := index(got, "analysis.ready_for_finalization")
		settles := 0
		for _, s := range got[:ri] {
			if s == "analyzer.succeeded" {
				settles++
			}
		}
		require.Equal(t, analyzers, settles)
		e.Close()
		_ = j
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{"MAX_PARALLEL_ANALYZERS": "3", "MAX_PARALLEL_JOBS": "5", "JOB_RETENTION": "2h"}
	c, err := ConfigFromEnv(func(k string) string { return env[k] })
	require.NoError(t, err)
	require.Equal(t, 3, c.MaxParallelAnalyzers)
	require.Equal(t, 5, c.MaxParallelJobs)
	require.Equal(t, 2*time.Hour, c.JobRetention)
	env["MAX_PARALLEL_JOBS"] = "0"
	_, err = ConfigFromEnv(func(k string) string { return env[k] })
	require.Error(t, err)
	env["MAX_PARALLEL_JOBS"] = "1"
	env["JOB_RETENTION"] = "soon"
	_, err = ConfigFromEnv(func(k string) string { return env[k] })
	require.Error(t, err)
}

func TestHTTPAdaptersThroughEngine(t *testing.T) {
	var deletes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sync", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"score": 7}`)) })
	mux.HandleFunc("POST /async", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		w.Write([]byte(`{"status_url":"http://` + r.Host + `/task"}`))
	})
	mux.HandleFunc("GET /task", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"running"}`)) })
	mux.HandleFunc("DELETE /task", func(w http.ResponseWriter, r *http.Request) { deletes.Add(1) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("SVC", srv.URL)
	y := `analyzers:
  - id: sync_ok
    execution: {adapter: http_sync, endpoint: "${SVC}/sync"}
  - id: sync_missing
    execution: {adapter: http_sync, endpoint: "${SVC}/nope"}
  - id: async_stuck
    execution: {adapter: http_async, endpoint: "${SVC}/async", timeout: 150ms, poll_interval: 10ms}
`
	e := newEngine(t, y, Config{}, nil)
	j, _ := e.Create(in("x"))
	fin := waitDone(t, e, j.ID)
	require.JSONEq(t, `{"score":7}`, string(ar(fin, "sync_ok").Result))
	require.Equal(t, job.CodeAdapterError, ar(fin, "sync_missing").Error.Code)
	require.NotContains(t, ar(fin, "sync_missing").Error.Message, srv.URL)
	require.Equal(t, job.CodeTimeout, ar(fin, "async_stuck").Error.Code)
	require.EqualValues(t, 1, deletes.Load())
}

func TestPanickingFinalizerFailsJobWithoutCrash(t *testing.T) {
	f := &fakeFinalizer{fn: func(context.Context) (rawMsg, error) { panic("bug") }}
	e := newEngine(t, regYAML(an("a", "ok"), finDef), Config{Finalizer: f}, map[string]adapter.Handler{"ok": ok, "final": ok})
	j, sub := createWithEvents(t, e, in("x"))
	got := types(drain(t, sub))
	require.Equal(t, "job.failed", got[len(got)-1])
	fin := waitDone(t, e, j.ID)
	require.Equal(t, job.StatusFailed, fin.Status)
	require.Equal(t, job.RunSucceeded, ar(fin, "a").Status, "settled results are kept")
}

func TestFinalizerIgnoringContextIsBoundedByTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	f := &fakeFinalizer{fn: func(context.Context) (rawMsg, error) {
		<-release // ignores ctx
		return rawMsg(`{"verdict":"low_risk","categories":[],"explanation":"late"}`), nil
	}}
	y := strings.Replace(finDef, "timeout: 1s", "timeout: 50ms", 1)
	e := newEngine(t, regYAML(an("a", "ok"), y), Config{Finalizer: f, MaxParallelJobs: 1}, map[string]adapter.Handler{"ok": ok, "final": ok})
	j, _ := e.Create(in("x"))
	start := time.Now()
	fin := waitDone(t, e, j.ID)
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, job.StatusCompleted, fin.Status)
	require.Equal(t, job.RunFailed, fin.Finalization.Status)
	require.Equal(t, job.CodeTimeout, fin.Finalization.Error.Code)
	require.Nil(t, fin.Finalization.Result, "late result is dropped")
	// the global slot is free again
	j2, _ := e.Create(CreateRequest{Input: job.Input{Content: "y"}, Analyzers: []string{"a"}, Finalize: job.Ptr(false)})
	require.Equal(t, job.StatusCompleted, waitDone(t, e, j2.ID).Status)
}
