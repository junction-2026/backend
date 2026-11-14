package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestJobStoreConfig(t *testing.T) {
	get := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }

	c, err := loadConfig(get(nil))
	require.NoError(t, err)
	require.Equal(t, "memory", c.JobStore, "memory is the default")

	c, err = loadConfig(get(map[string]string{"JOB_STORE": "sqlite", "JOB_HISTORY_RETENTION": "720h"}))
	require.NoError(t, err)
	require.Equal(t, "sqlite", c.JobStore)
	require.Equal(t, "data/jobs.db", c.JobStorePath)
	require.Equal(t, 720*time.Hour, c.Engine.HistoryRetention)

	c, err = loadConfig(get(map[string]string{"JOB_STORE": "SQLite", "JOB_STORE_PATH": "/x/y.db"}))
	require.NoError(t, err)
	require.Equal(t, "/x/y.db", c.JobStorePath)
	require.Zero(t, c.Engine.HistoryRetention, "history retention is off by default")

	c, err = loadConfig(get(map[string]string{"JOB_HISTORY_RETENTION": "1h"}))
	require.NoError(t, err)
	require.Zero(t, c.Engine.HistoryRetention, "the memory store has no history to prune")

	for vars, want := range map[string]string{"JOB_STORE": "JOB_STORE", "JOB_HISTORY_RETENTION": "JOB_HISTORY_RETENTION"} {
		_, err = loadConfig(get(map[string]string{vars: "nonsense"}))
		require.ErrorContains(t, err, want)
	}
	_, err = loadConfig(get(map[string]string{"JOB_HISTORY_RETENTION": "-1h"}))
	require.ErrorContains(t, err, "JOB_HISTORY_RETENTION")
}

func TestUnusableJobStorePathFailsStartup(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	err := run(context.Background(), env(map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers", "JOB_STORE": "sqlite",
		"JOB_STORE_PATH": filepath.Join(file, "jobs.db")}), io.Discard, nil) // a directory cannot be created below a file
	require.ErrorContains(t, err, "JOB_STORE_PATH")
}

// stop shuts the server down and leaves done usable for the cleanup of start.
func (s *server) restartable(t *testing.T) {
	t.Helper()
	s.stop()
	err := <-s.done
	require.NoError(t, err)
	s.done <- nil
}

func TestSQLiteHistorySurvivesRestart(t *testing.T) {
	vars := map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers", "JOB_STORE": "sqlite", "JOB_STORE_PATH": filepath.Join(t.TempDir(), "d", "jobs.db")}
	s := start(t, vars)
	_, created := s.json(t, "POST", "/api/v1/jobs", `{"input":{"content":"one two\nthree"}}`)
	id := created["id"].(string)
	var before map[string]any
	require.Eventually(t, func() bool {
		_, before = s.json(t, "GET", "/api/v1/jobs/"+id, "")
		return before["done"] == true
	}, 10*time.Second, 10*time.Millisecond)
	_, listBefore := s.json(t, "GET", "/api/v1/jobs", "")
	s.restartable(t)

	s = start(t, vars)
	code, after := s.json(t, "GET", "/api/v1/jobs/"+id, "")
	require.Equal(t, 200, code)
	require.Equal(t, before, after)
	_, listAfter := s.json(t, "GET", "/api/v1/jobs?limit=1", "")
	require.Equal(t, listBefore["items"], listAfter["items"])
	require.Nil(t, listAfter["next_cursor"])

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, s.wsURL(id), nil)
	require.NoError(t, err)
	_, b, err := c.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(b), `"type":"job.snapshot"`)
	require.Contains(t, string(b), `"status":"completed"`)
	_, _, err = c.Read(ctx)
	require.Equal(t, websocket.StatusNormalClosure, websocket.CloseStatus(err))

	code, body := s.json(t, "POST", "/api/v1/jobs/"+id+"/cancel", "")
	require.Equal(t, 409, code)
	require.Equal(t, "job_finished", body["error"].(map[string]any)["code"])
}
