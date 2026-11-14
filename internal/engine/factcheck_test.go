package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// factCheckEngine wires topic_extraction (stub LLM replying with claims) and fact_check (stub endpoint answering fcStatus).
func factCheckEngine(t *testing.T, claims string, fcStatus int) (*Engine, *atomic.Int32) {
	t.Helper()
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		c, _ := json.Marshal(`{"topics":[],"news":[],"claims":` + claims + `}`)
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":` + string(c) + `}}]}`))
	}))
	var fcCalls atomic.Int32
	fc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fcCalls.Add(1)
		w.WriteHeader(fcStatus)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(llm.Close)
	t.Cleanup(fc.Close)
	dir := t.TempDir()
	prompt := filepath.Join(dir, "p.md")
	require.NoError(t, os.WriteFile(prompt, []byte("extract"), 0o600))
	y := "analyzers:\n  - id: topic_extraction\n    execution: {adapter: llm, task: topic_extraction, base_url: " + llm.URL + ", api_key: k, model: m, prompt_file: " + prompt + "}\n" +
		"  - id: fact_check\n    depends_on: [topic_extraction]\n    input: {bindings: {extraction: $analyzers.topic_extraction}}\n" +
		"    execution: {adapter: fact_check, base_url: " + fc.URL + ", api_key: fc-secret-key}\n"
	path := filepath.Join(dir, "reg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(y), 0o600))
	reg, err := registry.Load(path, registry.Options{})
	require.NoError(t, err)
	e := New(reg, Config{Executor: &adapter.Executor{}})
	t.Cleanup(e.Close)
	return e, &fcCalls
}

func TestFactCheckSkipsOpinionOnlyAfterStarted(t *testing.T) {
	e, calls := factCheckEngine(t, `[]`, 200)
	j, sub := createWithEvents(t, e, in("politicians are all the same, wake up"))
	evs := drain(t, sub)
	fin := waitDone(t, e, j.ID)
	fc := ar(fin, "fact_check")
	require.Equal(t, job.RunSkipped, fc.Status)
	require.Equal(t, "no_checkable_claims", *fc.Reason)
	require.Nil(t, fc.Error)
	require.Nil(t, fc.Result)
	require.Equal(t, job.RunSucceeded, ar(fin, "topic_extraction").Status)
	names := typesFor(evs)
	require.Less(t, index(names, "analyzer.started:fact_check"), index(names, "analyzer.skipped:fact_check"))
	require.Zero(t, calls.Load())
}

func TestFactCheckRejectedKeyKeepsJobCompleted(t *testing.T) {
	e, calls := factCheckEngine(t, `[{"text":"X happened.","language":"en","query":"X happened","query_en":"X happened","entities":[],"check_worthiness":0.9}]`, 403)
	j, sub := createWithEvents(t, e, in("X happened."))
	evs := drain(t, sub)
	fin := waitDone(t, e, j.ID)
	fc := ar(fin, "fact_check")
	require.Equal(t, job.RunFailed, fc.Status)
	require.Equal(t, job.CodeAdapterError, fc.Error.Code)
	require.Contains(t, fc.Error.Message, "api_key")
	require.Equal(t, job.RunSucceeded, ar(fin, "topic_extraction").Status)
	require.Equal(t, job.StatusCompleted, fin.Status)
	require.EqualValues(t, 1, calls.Load())
	for _, ev := range evs { // the key is in no event
		b, _ := json.Marshal(ev.Data)
		require.NotContains(t, string(b), "fc-secret-key")
	}
}
