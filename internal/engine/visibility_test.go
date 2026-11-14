package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
)

func TestJobPublicDefaultsToTrue(t *testing.T) {
	e := newEngine(t, regYAML(an("a", "ok")), Config{}, map[string]adapter.Handler{"ok": ok})
	j, err := e.Create(in("x"))
	require.NoError(t, err)
	require.True(t, j.Public)
	u, err := e.Create(CreateRequest{Input: job.Input{Content: "x"}, Private: true})
	require.NoError(t, err)
	require.False(t, u.Public)
	waitDone(t, e, j.ID)
	waitDone(t, e, u.ID)
	require.Equal(t, []string{j.ID}, []string{e.List("")[0].ID})
	require.Len(t, e.List(""), 1, "the unlisted job is not listed")
}

func TestUnlistedJobByID(t *testing.T) {
	started := make(chan struct{}, 1)
	blk := func(ctx context.Context, _ adapter.Request) (any, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	e := newEngine(t, regYAML(an("hang", "blk")), Config{}, map[string]adapter.Handler{"blk": blk})
	j, sub := createWithEvents(t, e, CreateRequest{Input: job.Input{Content: "x"}, Private: true})
	<-started
	got, err := e.Get(j.ID)
	require.NoError(t, err)
	require.False(t, got.Public)

	c, err := e.Cancel(j.ID)
	require.NoError(t, err)
	require.False(t, c.Public)
	require.Equal(t, job.StatusCancelled, c.Status)

	evs := drain(t, sub)
	require.Equal(t, "job.snapshot", evs[0].Type)
	require.False(t, evs[0].Data.(map[string]any)["job"].(job.Job).Public)
	last := evs[len(evs)-1]
	require.Equal(t, "job.cancelled", last.Type)
	require.False(t, last.Data.(map[string]any)["job"].(job.Job).Public)
}

func TestUnlistedJobSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	yaml := regYAML(an("a", "ok"), finDef)
	h := map[string]adapter.Handler{"ok": ok, "final": ok}
	e1, st1 := sqliteEngine(t, path, yaml, Config{Finalizer: &fakeFinalizer{}}, h)
	pub, err := e1.Create(in("x"))
	require.NoError(t, err)
	unl, err := e1.Create(CreateRequest{Input: job.Input{Content: "x"}, Private: true})
	require.NoError(t, err)
	waitDone(t, e1, pub.ID)
	waitDone(t, e1, unl.ID)
	require.Eventually(t, func() bool { _, ok, _ := st1.Load(unl.ID); return ok }, 5*time.Second, 5*time.Millisecond)
	crash(t, e1, st1)

	e2, _ := sqliteEngine(t, path, yaml, Config{Finalizer: &fakeFinalizer{}}, h)
	_, err = e2.Recover()
	require.NoError(t, err)
	got, err := e2.Get(unl.ID)
	require.NoError(t, err)
	require.False(t, got.Public)
	require.False(t, snapshotOf(t, e2, unl.ID).Data.(map[string]any)["job"].(job.Job).Public)
	list := e2.List("")
	require.Len(t, list, 1)
	require.Equal(t, pub.ID, list[0].ID)
	_, err = e2.Cancel(unl.ID)
	require.ErrorIs(t, err, ErrJobFinished)
}
