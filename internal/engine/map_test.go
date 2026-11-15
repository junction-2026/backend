package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
)

// a job is on the map as soon as it reports done, with the Memory store and with SQLite (also after a restart).
func TestMapJobCountedOnceDone(t *testing.T) {
	yaml := regYAML(an("source_info", "outlet"), an("techniques", "tech"))
	h := map[string]adapter.Handler{
		"outlet": func(context.Context, adapter.Request) (any, error) {
			return map[string]any{"page_outlet": map[string]string{"name": "O", "domain": "o.pl", "country_code": "PL", "country": "Poland"}}, nil
		},
		"tech": func(context.Context, adapter.Request) (any, error) {
			return map[string]any{"detected": []map[string]string{{"name": "Doubt", "display": "Doubt"}}}, nil
		},
	}
	path := filepath.Join(t.TempDir(), "jobs.db")
	e, st := sqliteEngine(t, path, yaml, Config{}, h)
	j, err := e.Create(in("x"))
	require.NoError(t, err)
	private, err := e.Create(CreateRequest{Input: job.Input{Content: "x"}, Private: true})
	require.NoError(t, err)
	waitDone(t, e, j.ID)
	waitDone(t, e, private.ID)

	got, _, err := e.MapCountries()
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "PL", got[0].Code)
	require.Equal(t, 1, got[0].Analyses, "the unlisted job is not counted")
	items, err := e.MapAnalyses("PL", nil, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, j.ID, items[0].Job.ID)

	crash(t, e, st)
	e2, _ := sqliteEngine(t, path, yaml, Config{}, h)
	_, err = e2.Recover()
	require.NoError(t, err)
	got, _, err = e2.MapCountries()
	require.NoError(t, err)
	require.Equal(t, 1, got[0].Analyses)

	e3 := newEngine(t, yaml, Config{}, h) // Memory store
	m, err := e3.Create(in("x"))
	require.NoError(t, err)
	waitDone(t, e3, m.ID)
	got, _, err = e3.MapCountries()
	require.NoError(t, err)
	require.Equal(t, 1, got[0].Analyses)
}
