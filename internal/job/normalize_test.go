package job

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ name, in, ct, want string }{
		{"nfc", "é", "text/plain", "é"},
		{"line endings", "a\r\nb\rc\nd", "text/plain", "a\nb\nc\nd"},
		{"trim", "  \n hi \t\n", "text/markdown", "hi"},
		{"html tags removed", "<p>Hello <b>big</b> world</p>", "text/html", "Hello big world"},
		{"html entities and params", "<div>a &amp; b</div>", "Text/HTML; charset=utf-8", "a & b"},
		{"html skips script and style", "<style>p{}</style>x<script>alert(1)</script>y", "text/html", "xy"},
		{"html paragraphs do not glue", "<p>one</p><p>two</p>", "text/html", "one\ntwo"},
		{"plain keeps markup", "<b>x</b>", "text/plain", "<b>x</b>"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, Normalize(c.in, c.ct), c.name)
	}
}

func TestCoverageAndJSONShape(t *testing.T) {
	runs := []AnalyzerRun{
		{Status: RunSkipped, Reason: Str(ReasonNotSelected)}, {Status: RunSucceeded}, {Status: RunSucceeded},
		{Status: RunFailed}, {Status: RunRunning}, {Status: RunSkipped, Reason: Str(ReasonDependencyFailed)},
	}
	require.Equal(t, Coverage{Total: 6, Applicable: 5, Pending: 0, Running: 1, Succeeded: 2, Failed: 1, Skipped: 2, Settled: 5}, ComputeCoverage(runs))

	ts := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	j := Job{ID: "j", Status: StatusQueued, CreatedAt: ts, Input: &Input{Content: "x", ContentType: "text/plain", NormalizedContent: "x"}, Analyzers: []AnalyzerRun{{ID: "a", DependsOn: []string{}}}}
	b, _ := json.Marshal(j)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	require.Equal(t, "2026-11-14T10:00:00Z", m["created_at"])
	require.Contains(t, m, "finished_at")
	require.Nil(t, m["finished_at"])
	require.Equal(t, map[string]any{"content": "x", "content_type": "text/plain", "source": map[string]any{}}, m["input"])
	a := m["analyzers"].([]any)[0].(map[string]any)
	require.Nil(t, a["result"])
	require.Contains(t, a, "reason")
	require.Contains(t, a, "duration_ms")
}

func TestCloneIsIndependent(t *testing.T) {
	j := Job{Analyzers: []AnalyzerRun{{ID: "a", Status: RunPending}}}
	c := j.Clone()
	j.Analyzers[0].Status = RunSucceeded
	require.Equal(t, RunPending, c.Analyzers[0].Status)
}
