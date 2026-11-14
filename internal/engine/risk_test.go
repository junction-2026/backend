package engine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
	"github.com/junction-2026/backend/internal/risk"
)

// Handlers returning the result shapes of the techniques and propaganda_detection analyzers.
func techniquesResult(context.Context, adapter.Request) (any, error) {
	var sc []map[string]any
	for _, n := range registry.TechniqueNames {
		s := 0.0
		if n == "Loaded_Language" {
			s = 0.9
		}
		sc = append(sc, map[string]any{"label": n, "display": n, "score": s, "known": true})
	}
	return map[string]any{"words": 300, "results": []any{map[string]any{"model": "nci-technique", "cutoff": 0.5, "scores": sc}}}, nil
}

func propagandaResult(context.Context, adapter.Request) (any, error) {
	return map[string]any{"result": []any{map[string]any{"label": "PROPAGANDA", "score": 0.8}}}, nil
}

var riskHandlers = map[string]adapter.Handler{"techniques": techniquesResult, "propaganda": propagandaResult, "ok": ok, "block": block, "final": ok}

func riskReg(extra ...string) string {
	return regYAML(append([]string{an("techniques", "techniques"), an("propaganda_detection", "propaganda"), an("other", "ok")}, extra...)...)
}

// riskOf is the event's data.risk, which must be the raw JSON of the job's RiskScore.
func riskOf(t *testing.T, ev events.Event) json.RawMessage {
	t.Helper()
	r, ok := data(ev)["risk"].(json.RawMessage)
	require.True(t, ok, "data.risk of %s is the stored RiskScore", ev.Type)
	return r
}

func TestRiskAtTheBarrierWithFinalizer(t *testing.T) {
	f := &fakeFinalizer{}
	e := newEngine(t, riskReg(finDef), Config{Finalizer: f}, riskHandlers)
	jb, sub := createWithEvents(t, e, in("x"))
	require.Nil(t, jb.Risk, "no risk before the barrier")
	evs := drain(t, sub)
	got := typesFor(evs)
	require.Equal(t, []string{"analysis.ready_for_finalization", "analysis.finalizing", "analysis.finalized", "job.completed"}, got[len(got)-4:])
	require.Equal(t, 1, count(evs, "analysis.ready_for_finalization"))
	ready := evs[len(got)-4]
	done := waitDone(t, e, jb.ID)

	require.NotEmpty(t, done.Risk)
	require.JSONEq(t, string(done.Risk), string(riskOf(t, ready)))
	require.Equal(t, string(done.Risk), string(riskOf(t, ready)), "the same bytes")
	require.Equal(t, done.Risk, data(evs[len(got)-1])["job"].(job.Job).Risk, "data.job.risk of the terminal event equals Job.risk")
	require.Equal(t, done.Risk, f.in.Job.Risk, "set before finalization starts")

	var rs struct {
		Status string
		Score  float64
		Band   string
	}
	require.NoError(t, json.Unmarshal(done.Risk, &rs))
	require.Equal(t, "scored", rs.Status)
	require.NotEmpty(t, rs.Band)

	again, err := e.Get(jb.ID)
	require.NoError(t, err)
	require.Equal(t, done.Risk, again.Risk)
	for _, ev := range evs[:len(got)-4] { // every earlier event carries a Job without risk or none at all
		if j, ok := data(ev)["job"].(job.Job); ok {
			require.Nil(t, j.Risk)
		}
	}
}

func TestRiskUnchangedByFinalizerFailure(t *testing.T) {
	f := &fakeFinalizer{fn: func(context.Context) (json.RawMessage, error) {
		return nil, &job.RunError{Code: job.CodeTimeout, Message: "slow"}
	}}
	e := newEngine(t, riskReg(finDef), Config{Finalizer: f}, riskHandlers)
	jb, sub := createWithEvents(t, e, in("x"))
	evs := drain(t, sub)
	require.Contains(t, types(evs), "analysis.finalization_failed")
	done := waitDone(t, e, jb.ID)
	require.Equal(t, job.StatusCompleted, done.Status)
	require.Equal(t, string(riskOf(t, evs[index(types(evs), "analysis.ready_for_finalization")])), string(done.Risk))
}

func TestRiskWithoutFinalizer(t *testing.T) {
	e := newEngine(t, riskReg(), Config{}, riskHandlers)
	jb, sub := createWithEvents(t, e, in("x"))
	evs := drain(t, sub)
	got := types(evs)
	require.Equal(t, []string{"analysis.ready_for_finalization", "job.completed"}, got[len(got)-2:], "no event added, removed or reordered")
	done := waitDone(t, e, jb.ID)
	require.NotEmpty(t, done.Risk)
	require.Equal(t, string(done.Risk), string(riskOf(t, evs[len(got)-2])))
	require.Equal(t, done.Risk, data(evs[len(got)-1])["job"].(job.Job).Risk)
}

func TestRiskUnavailableWhenNoInputRanButStillSet(t *testing.T) {
	e := newEngine(t, regYAML(an("a", "ok")), Config{}, riskHandlers)
	jb, _ := createWithEvents(t, e, in("x"))
	done := waitDone(t, e, jb.ID)
	var rs struct{ Status, Reason string }
	require.NoError(t, json.Unmarshal(done.Risk, &rs))
	require.Equal(t, "unavailable", rs.Status)
	require.Equal(t, "no_content_signals", rs.Reason)
}

func TestRiskUsesConfiguredSettings(t *testing.T) {
	cfg := risk.Default()
	cfg.DampeningFactor = 0.5
	e := newEngine(t, riskReg(), Config{Risk: cfg}, riskHandlers)
	jb, _ := createWithEvents(t, e, in("x"))
	var rs struct {
		Config struct {
			DampeningFactor float64 `json:"dampening_factor"`
		}
	}
	require.NoError(t, json.Unmarshal(waitDone(t, e, jb.ID).Risk, &rs))
	require.Equal(t, 0.5, rs.Config.DampeningFactor)
}

func TestRiskNullWhenCancelledBeforeTheBarrier(t *testing.T) {
	e := newEngine(t, riskReg(an("hang", "block")), Config{}, riskHandlers)
	j, err := e.Create(in("x"))
	require.NoError(t, err)
	waitFor(t, e, j.ID, func(j job.Job) bool {
		return ar(j, "hang").Status == job.RunRunning && ar(j, "techniques").Status == job.RunSucceeded
	})
	running, _ := e.Get(j.ID)
	require.Nil(t, running.Risk, "a running job has no risk")
	_, err = e.Cancel(j.ID)
	require.NoError(t, err)
	done := waitDone(t, e, j.ID)
	require.Equal(t, job.StatusCancelled, done.Status)
	require.Nil(t, done.Risk)
}

func TestRiskSurvivesRestartAndInterruptedJobHasNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	yaml := riskReg(finDef)
	e1, st1 := sqliteEngine(t, path, yaml, Config{Finalizer: &fakeFinalizer{}}, riskHandlers)
	j, err := e1.Create(in("x"))
	require.NoError(t, err)
	done := waitDone(t, e1, j.ID)
	require.NotEmpty(t, done.Risk)

	crash(t, e1, st1)

	e2, _ := sqliteEngine(t, path, yaml, Config{Finalizer: &fakeFinalizer{}}, riskHandlers)
	n, err := e2.Recover()
	require.NoError(t, err)
	require.Zero(t, n)
	got, err := e2.Get(j.ID)
	require.NoError(t, err)
	require.Equal(t, string(done.Risk), string(got.Risk), "verbatim")
	require.Equal(t, string(done.Risk), string(riskOf(t, snapshotEventRisk(t, e2, j.ID))), "the snapshot's job carries it too")
}

func snapshotEventRisk(t *testing.T, e *Engine, id string) events.Event {
	ev := snapshotOf(t, e, id)
	return events.Event{Type: ev.Type, Data: map[string]any{"risk": ev.Data.(map[string]any)["job"].(job.Job).Risk}}
}

func TestInterruptedJobKeepsRiskNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	yaml := riskReg(an("hang", "block"))
	e1, st1 := sqliteEngine(t, path, yaml, Config{}, riskHandlers)
	j, err := e1.Create(in("x"))
	require.NoError(t, err)
	waitFor(t, e1, j.ID, func(j job.Job) bool { return ar(j, "hang").Status == job.RunRunning })
	crash(t, e1, st1)

	e2, _ := sqliteEngine(t, path, yaml, Config{}, riskHandlers)
	n, err := e2.Recover()
	require.NoError(t, err)
	require.Equal(t, 1, n)
	got, err := e2.Get(j.ID)
	require.NoError(t, err)
	require.Equal(t, job.StatusFailed, got.Status)
	require.Nil(t, got.Risk)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	require.JSONEq(t, "null", string(m["risk"]))
}
