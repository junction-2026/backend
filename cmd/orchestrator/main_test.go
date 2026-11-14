package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

type server struct {
	base string
	stop context.CancelFunc
	done chan error
}

// start runs the whole process (run) on a free port.
func start(t *testing.T, vars map[string]string) *server {
	t.Helper()
	return startLog(t, vars, io.Discard)
}

// startLog is start with the process log written to logOut.
func startLog(t *testing.T, vars map[string]string, logOut io.Writer) *server {
	t.Helper()
	if vars["LISTEN_ADDR"] == "" {
		vars["LISTEN_ADDR"] = "127.0.0.1:0"
	}
	ctx, stop := context.WithCancel(context.Background())
	s := &server{stop: stop, done: make(chan error, 1)}
	ready := make(chan string, 1)
	go func() { s.done <- run(ctx, env(vars), logOut, func(a string) { ready <- a }) }()
	select {
	case addr := <-ready:
		s.base = "http://" + addr
	case err := <-s.done:
		t.Fatalf("server did not start: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}
	t.Cleanup(func() { stop(); <-s.done })
	return s
}

func (s *server) json(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, s.base+path, strings.NewReader(body))
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var m map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&m))
	return res.StatusCode, m
}

func (s *server) wsURL(id string) string {
	return "ws" + strings.TrimPrefix(s.base, "http") + "/api/v1/jobs/" + id + "/events"
}

func TestConfigDefaults(t *testing.T) {
	c, err := loadConfig(func(string) string { return "" })
	require.NoError(t, err)
	require.Equal(t, ":8080", c.ListenAddr)
	require.Equal(t, "/etc/orchestrator/analyzers", c.AnalyzersConfig)
	require.Equal(t, 20*time.Second, c.LocalizationTimeout)
}

func TestLocalizationTimeoutConfig(t *testing.T) {
	c, err := loadConfig(func(k string) string { return map[string]string{"LOCALIZATION_TIMEOUT": "5s"}[k] })
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, c.LocalizationTimeout)
	for _, v := range []string{"-1s", "0s", "soon"} {
		_, err := loadConfig(func(k string) string { return map[string]string{"LOCALIZATION_TIMEOUT": v}[k] })
		require.ErrorContains(t, err, "LOCALIZATION_TIMEOUT", v)
	}
}

func TestConfigErrorsAreClear(t *testing.T) {
	_, err := loadConfig(func(k string) string {
		if k == "MAX_PARALLEL_JOBS" {
			return "zero"
		}
		return ""
	})
	require.ErrorContains(t, err, "MAX_PARALLEL_JOBS")

	empty := t.TempDir()
	missing := filepath.Join(t.TempDir(), "nope")
	for name, c := range map[string]struct {
		vars map[string]string
		want string
	}{
		"missing registry":  {map[string]string{"ANALYZERS_CONFIG": missing}, missing},
		"empty registry":    {map[string]string{"ANALYZERS_CONFIG": empty}, empty},
		"bad engine config": {map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers", "JOB_RETENTION": "-1h"}, "JOB_RETENTION"},
		"bad listen addr":   {map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers", "LISTEN_ADDR": "not-an-address"}, "not-an-address"},
		"unset env in registry": {map[string]string{"ANALYZERS_CONFIG": writeRegistry(t, `analyzers:
  - id: a
    execution: {adapter: http_sync, endpoint: "${NO_SUCH_VAR}"}
`)}, "NO_SUCH_VAR"},
	} {
		t.Run(name, func(t *testing.T) {
			err := run(context.Background(), env(c.vars), io.Discard, nil)
			require.ErrorContains(t, err, c.want)
		})
	}
}

func writeRegistry(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "r.yaml"), []byte(yaml), 0o600))
	return dir
}

// The sample registry shipped in the image: text_stats only, no finalizer.
func TestEndToEndTextStats(t *testing.T) {
	s := start(t, map[string]string{"ANALYZERS_CONFIG": "../../config/analyzers"})

	code, health := s.json(t, "GET", "/healthz", "")
	require.Equal(t, 200, code)
	require.Equal(t, map[string]any{"status": "ok"}, health)

	_, list := s.json(t, "GET", "/api/v1/analyzers", "")
	items := list["items"].([]any)
	require.Len(t, items, 1)
	require.Equal(t, "text_stats", items[0].(map[string]any)["id"])

	code, created := s.json(t, "POST", "/api/v1/jobs", `{"input":{"content":"one two\nthree"}}`)
	require.Equal(t, 201, code)
	id := created["id"].(string)
	var j map[string]any
	require.Eventually(t, func() bool {
		_, j = s.json(t, "GET", "/api/v1/jobs/"+id, "")
		return j["done"] == true
	}, 10*time.Second, 10*time.Millisecond)
	require.Equal(t, "completed", j["status"])
	run := j["analyzers"].([]any)[0].(map[string]any)
	require.Equal(t, "succeeded", run["status"])
	require.Equal(t, map[string]any{"characters": 13.0, "words": 3.0, "lines": 2.0}, run["result"])
	fin := j["finalization"].(map[string]any)
	require.Equal(t, "skipped", fin["status"])
	require.Equal(t, "not_configured", fin["reason"])

	code, page := s.json(t, "GET", "/api/v1/jobs?status=completed", "")
	require.Equal(t, 200, code)
	require.Len(t, page["items"], 1)

	// the finished job streams just its snapshot, then 1000
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, s.wsURL(id), nil)
	require.NoError(t, err)
	_, b, err := c.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(b), `"type":"job.snapshot"`)
	_, _, err = c.Read(ctx)
	require.Equal(t, websocket.StatusNormalClosure, websocket.CloseStatus(err))
}

func TestGracefulShutdown(t *testing.T) {
	aborted := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // the server only notices a disconnect after the body was read
		<-r.Context().Done()               // never answers; ends when the orchestrator aborts the call
		close(aborted)
	}))
	defer slow.Close()
	dir := writeRegistry(t, `analyzers:
  - id: slow
    execution: {adapter: http_sync, endpoint: "${SLOW_URL}", timeout: 60s}
`)
	s := start(t, map[string]string{"ANALYZERS_CONFIG": dir, "SLOW_URL": slow.URL})

	_, created := s.json(t, "POST", "/api/v1/jobs", `{"input":{"content":"x"}}`)
	id := created["id"].(string)
	require.Eventually(t, func() bool {
		_, j := s.json(t, "GET", "/api/v1/jobs/"+id, "")
		return j["analyzers"].([]any)[0].(map[string]any)["status"] == "running"
	}, 5*time.Second, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, s.wsURL(id), nil)
	require.NoError(t, err)
	_, _, err = c.Read(ctx) // snapshot
	require.NoError(t, err)

	start := time.Now()
	s.stop() // what SIGTERM does
	_, _, err = c.Read(ctx)
	require.Equal(t, websocket.StatusGoingAway, websocket.CloseStatus(err), "%v", err)
	select {
	case err := <-s.done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return within 10s")
	}
	require.Less(t, time.Since(start), 10*time.Second)
	select {
	case <-aborted:
	case <-time.After(5 * time.Second):
		t.Fatal("the running analyzer call was not cancelled")
	}
	s.done <- nil // t.Cleanup waits for a value
	_, err = http.Get(s.base + "/healthz")
	require.Error(t, err, "listener is closed")
}
