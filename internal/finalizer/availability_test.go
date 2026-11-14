package finalizer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

func availabilityRuntime(t *testing.T) *Runtime {
	y := "analyzers:\n"
	for _, id := range []string{"topic_extraction", "fact_check", "news_search", "source_info"} {
		y += "  - id: " + id + "\n    execution: {adapter: custom, handler: ok}\n    llm: {include: true, description: d}\n"
	}
	p := filepath.Join(t.TempDir(), "r.yaml")
	require.NoError(t, os.WriteFile(p, []byte(y), 0o600))
	reg, err := registry.Load(p, registry.Options{HandlerExists: func(string) bool { return true }})
	require.NoError(t, err)
	return &Runtime{Reg: reg}
}

func entriesByID(t *testing.T, raw []json.RawMessage) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, b := range raw {
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		out[m["id"].(string)] = m
	}
	return out
}

func TestEntriesNothingToCheckAreNotApplicable(t *testing.T) {
	j := &job.Job{Analyzers: []job.AnalyzerRun{
		{ID: "topic_extraction", Status: job.RunSucceeded, Result: json.RawMessage(`{"topics":[],"news":[],"claims":[]}`)},
		{ID: "fact_check", Status: job.RunSkipped, Reason: job.Str("no_checkable_claims"), DependsOn: []string{"topic_extraction"}},
		{ID: "news_search", Status: job.RunSkipped, Reason: job.Str("no_searchable_topics"), DependsOn: []string{"topic_extraction"}},
		{ID: "source_info", Status: job.RunSkipped, Reason: job.Str("dependency_skipped"), DependsOn: []string{"news_search"}},
	}}
	raw, anySucceeded := availabilityRuntime(t).llmEntries(j)
	require.True(t, anySucceeded)
	e := entriesByID(t, raw)
	require.Equal(t, "ran", e["topic_extraction"]["availability"])
	require.NotContains(t, e["topic_extraction"], "note")
	for _, id := range []string{"fact_check", "news_search", "source_info"} {
		require.Equal(t, "not_applicable", e[id]["availability"], id)
		require.Equal(t, "skipped", e[id]["status"], id)
	}
	require.Equal(t, "dependency_skipped", e["source_info"]["reason"])
	require.Equal(t, e["news_search"]["note"], e["source_info"]["note"])
}

func TestEntriesFailedDependencyAreUnavailable(t *testing.T) {
	j := &job.Job{Analyzers: []job.AnalyzerRun{
		{ID: "news_search", Status: job.RunFailed, Error: &job.RunError{Code: job.CodeAdapterError, Message: "x"}},
		{ID: "source_info", Status: job.RunSkipped, Reason: job.Str("dependency_failed"), DependsOn: []string{"news_search"}},
	}}
	raw, anySucceeded := availabilityRuntime(t).llmEntries(j)
	require.False(t, anySucceeded, "insufficient-evidence rule unchanged")
	e := entriesByID(t, raw)
	require.Equal(t, map[string]any{"id": "news_search", "description": "d", "status": "failed", "reason": "adapter_error",
		"availability": "unavailable", "note": job.NoteUnavailable}, e["news_search"])
	require.Equal(t, map[string]any{"id": "source_info", "description": "d", "status": "skipped", "reason": "dependency_failed",
		"availability": "unavailable", "note": job.NoteUnavailable}, e["source_info"])
}
