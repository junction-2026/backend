package finalizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

const okBody = `{"verdict":"high_risk","overall_score":82,"categories":[{"id":"misleading_claims","score":70,"x":1}],"explanation":"because","extra":{"b":2,"a":[1.50]}}`

// stub is the finalizer service.
type stub struct {
	*httptest.Server
	calls   atomic.Int32
	started chan struct{} // receives once per call
	mu      sync.Mutex
	bodies  [][]byte
	header  http.Header
	handle  func(w http.ResponseWriter, r *http.Request)
}

func newStub(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) *stub {
	s := &stub{started: make(chan struct{}, 1000), handle: h}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies, s.header = append(s.bodies, b), r.Header.Clone()
		s.mu.Unlock()
		s.calls.Add(1)
		s.started <- struct{}{}
		s.handle(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func reply(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }
}

func blockUntilGone(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }

func an(id, handler string) string {
	return fmt.Sprintf("  - id: %s\n    execution: {adapter: custom, handler: %s}\n", id, handler)
}

func finYAML(extra string) string {
	return "  - id: final_synthesis\n    trigger: after_all_analyzers_settled\n    input:\n      bindings: {content: $input, results: $job.analyzers, coverage: $job.coverage}\n" +
		"    execution: {adapter: http_sync, endpoint: \"${FINALIZER_URL}\", timeout: 5s}\n" + extra
}

func handlers(m map[string]adapter.Handler) *adapter.Handlers {
	h := adapter.NewHandlers()
	for n, fn := range m {
		h.Register(n, fn)
	}
	return h
}

func okHandler(_ context.Context, req adapter.Request) (any, error) {
	return map[string]any{"by": req.AnalyzerID, "score": 12}, nil
}

func failHandler(context.Context, adapter.Request) (any, error) { return nil, errors.New("boom") }

func hold(release chan struct{}) adapter.Handler {
	return func(ctx context.Context, req adapter.Request) (any, error) {
		select {
		case <-release:
			return okHandler(ctx, req)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func load(t *testing.T, yaml, url string, h *adapter.Handlers) *registry.Registry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reg.yaml")
	require.NoError(t, os.WriteFile(path, []byte("analyzers:\n"+yaml), 0o600))
	reg, err := registry.Load(path, registry.Options{HandlerExists: h.Has,
		Getenv: func(k string) (string, bool) { return url, k == "FINALIZER_URL" }})
	require.NoError(t, err)
	return reg
}

func newEngine(t *testing.T, yaml string, s *stub, h map[string]adapter.Handler, cfg engine.Config) *engine.Engine {
	t.Helper()
	hs := handlers(h)
	reg := load(t, yaml, s.URL, hs)
	exec := &adapter.Executor{Handlers: hs}
	cfg.Executor = exec
	cfg.Finalizer = New(reg, exec, nil)
	e := engine.New(reg, cfg)
	t.Cleanup(e.Close)
	return e
}

func create(t *testing.T, e *engine.Engine, req engine.CreateRequest) (job.Job, *events.Subscription) {
	t.Helper()
	j, err := e.Create(req)
	require.NoError(t, err)
	sub, err := e.Subscribe(j.ID)
	require.NoError(t, err)
	return j, sub
}

func req(content string) engine.CreateRequest {
	return engine.CreateRequest{Input: job.Input{Content: content}}
}

func drain(t *testing.T, sub *events.Subscription) []events.Event {
	t.Helper()
	var out []events.Event
	timer := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-sub.Events():
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timer:
			t.Fatal("event stream did not end")
		}
	}
}

func types(evs []events.Event) (out []string) {
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return
}

func count(evs []events.Event, typ string) (n int) {
	for _, t := range types(evs) {
		if t == typ {
			n++
		}
	}
	return
}

func idx(evs []events.Event, typ string) int {
	for i, t := range types(evs) {
		if t == typ {
			return i
		}
	}
	return -1
}

func waitDone(t *testing.T, e *engine.Engine, id string) job.Job {
	t.Helper()
	var j job.Job
	require.Eventually(t, func() bool { j, _ = e.Get(id); return j.Done }, 10*time.Second, 2*time.Millisecond)
	return j
}

// run drives one job with analyzers ok1, ok2 against the stub to its end.
func run(t *testing.T, s *stub, extraFin string, r engine.CreateRequest) (job.Job, []events.Event) {
	t.Helper()
	e := newEngine(t, an("ok1", "ok")+an("ok2", "ok")+finYAML(extraFin), s, map[string]adapter.Handler{"ok": okHandler}, engine.Config{})
	_, sub := create(t, e, r)
	evs := drain(t, sub)
	j, err := e.Get(evs[len(evs)-1].Data.(map[string]any)["job"].(job.Job).ID)
	require.NoError(t, err)
	return j, evs
}

func TestSuccessNormalizesAndKeepsUnknownMembers(t *testing.T) {
	s := newStub(t, reply(okBody))
	j, evs := run(t, s, "", req("Hello  world"))
	require.Equal(t, job.StatusCompleted, j.Status)
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	require.EqualValues(t, 1, s.calls.Load())
	var res map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(j.Finalization.Result, &res))
	require.JSONEq(t, `"finalizer"`, string(res["produced_by"]))
	require.JSONEq(t, `[]`, string(res["evidence"]))
	require.JSONEq(t, `[]`, string(res["limitations"]))
	require.JSONEq(t, `{"b":2,"a":[1.50]}`, string(res["extra"]))
	require.JSONEq(t, `[{"id":"misleading_claims","score":70,"x":1}]`, string(res["categories"]))
	require.JSONEq(t, `82`, string(res["overall_score"]))
	require.Contains(t, string(res["extra"]), "1.50", "numbers stay verbatim")
	require.Equal(t, []string{"job.snapshot", "job.started"}, types(evs)[:2])
	require.Less(t, idx(evs, "analysis.finalized"), idx(evs, "job.completed"))
	require.Equal(t, 1, count(evs, "analysis.finalizing"))
	require.Equal(t, 1, count(evs, "analysis.finalized"))
	require.Zero(t, count(evs, "analysis.finalization_failed"))
	// REST state matches the last finalization event.
	last := evs[idx(evs, "analysis.finalized")].Data.(map[string]any)["finalization"].(job.Finalization)
	require.Equal(t, last.Status, j.Finalization.Status)
}

func TestMinimalResponseDefaults(t *testing.T) {
	s := newStub(t, reply(`{"verdict":"low_risk","categories":[],"explanation":"No signs found.","produced_by":"orchestrator"}`))
	j, _ := run(t, s, "", req("x"))
	require.JSONEq(t, `{"verdict":"low_risk","overall_score":null,"categories":[],"explanation":"No signs found.","evidence":[],"limitations":[],"produced_by":"finalizer"}`, string(j.Finalization.Result))
}

func TestRequestInputsAndHeaders(t *testing.T) {
	s := newStub(t, reply(okBody))
	j, _ := run(t, s, "", engine.CreateRequest{Input: job.Input{Content: "<p>Hi</p>", ContentType: "text/html"}})
	var body struct {
		AnalyzerID string `json:"analyzer_id"`
		Inputs     struct {
			Content  struct{ Content string }
			Results  []job.AnalyzerRun
			Coverage job.Coverage
		}
	}
	require.Len(t, s.bodies, 1)
	raw := string(s.bodies[0])
	require.Contains(t, raw, `"normalized_content":"Hi"`)
	require.NoError(t, json.Unmarshal(s.bodies[0], &body))
	require.Equal(t, "<p>Hi</p>", body.Inputs.Content.Content)
	require.Equal(t, "final_synthesis", body.AnalyzerID)
	require.Equal(t, j.ID, s.header.Get("X-Analysis-Job-Id"))
	require.Len(t, body.Inputs.Results, 2)
	for _, r := range body.Inputs.Results {
		require.Equal(t, job.RunSucceeded, r.Status)
	}
	require.Equal(t, 2, body.Inputs.Coverage.Settled)
}

func TestHTTP5xxIsAdapterError(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "SECRET body", 500) })
	e := newEngine(t, an("ok1", "ok")+finYAML(""), s, map[string]adapter.Handler{"ok": okHandler}, engine.Config{})
	before := runsOf(t, e, req("x"))
	require.Equal(t, job.RunFailed, before.Finalization.Status)
	require.Equal(t, job.CodeAdapterError, before.Finalization.Error.Code)
	require.NotContains(t, before.Finalization.Error.Message, "SECRET")
	require.Equal(t, job.StatusCompleted, before.Status)
	require.Equal(t, job.RunSucceeded, before.Analyzers[0].Status)
	require.NotNil(t, before.Analyzers[0].Result)
}

func runsOf(t *testing.T, e *engine.Engine, r engine.CreateRequest) job.Job {
	t.Helper()
	j, sub := create(t, e, r)
	drain(t, sub)
	got, err := e.Get(j.ID)
	require.NoError(t, err)
	return got
}

func TestTimeout(t *testing.T) {
	s := newStub(t, blockUntilGone)
	yaml := an("ok1", "ok") + strings.Replace(finYAML(""), "timeout: 5s", "timeout: 150ms", 1)
	e := newEngine(t, yaml, s, map[string]adapter.Handler{"ok": okHandler}, engine.Config{})
	j := runsOf(t, e, req("x"))
	require.Equal(t, job.RunFailed, j.Finalization.Status)
	require.Equal(t, job.CodeTimeout, j.Finalization.Error.Code)
	require.Equal(t, job.StatusCompleted, j.Status)
	require.Equal(t, job.RunSucceeded, j.Analyzers[0].Status)
}

func TestDefaultTimeoutIs60s(t *testing.T) {
	h := handlers(map[string]adapter.Handler{"ok": okHandler})
	y := an("ok1", "ok") + "  - id: f\n    trigger: after_all_analyzers_settled\n    execution: {adapter: http_sync, endpoint: \"http://x\"}\n"
	require.Equal(t, 60*time.Second, load(t, y, "", h).Finalizer().Execution.Timeout)
	require.Equal(t, 30*time.Second, load(t, an("ok1", "ok"), "", h).Get("ok1").Execution.Timeout)
}

func TestInvalidOutputCases(t *testing.T) {
	schema := "    output:\n      schema: {type: object, properties: {explanation: {minLength: 10}}}\n"
	cases := []struct {
		name, body, schema, member string
	}{
		{"missing members", `{"verdict":"low_risk"}`, "", "categories"},
		{"missing verdict", `{"categories":[],"explanation":"e"}`, "", "verdict"},
		{"bad verdict", `{"verdict":"scary","categories":[],"explanation":"e"}`, "", "verdict"},
		{"categories not array", `{"verdict":"low_risk","categories":{},"explanation":"e"}`, "", "categories"},
		{"explanation not string", `{"verdict":"low_risk","categories":[],"explanation":3}`, "", "explanation"},
		{"score too high", `{"verdict":"low_risk","overall_score":101,"categories":[],"explanation":"e"}`, "", "overall_score"},
		{"score not number", `{"verdict":"low_risk","overall_score":"9","categories":[],"explanation":"e"}`, "", "overall_score"},
		{"category score negative", `{"verdict":"low_risk","categories":[{"id":"a","score":-1}],"explanation":"e"}`, "", "categories[0].score"},
		{"insufficient with score", `{"verdict":"insufficient_evidence","overall_score":40,"categories":[],"explanation":"e"}`, "", "overall_score"},
		{"evidence not array", `{"verdict":"low_risk","categories":[],"explanation":"e","evidence":{}}`, "", "evidence"},
		{"schema violation", `{"verdict":"low_risk","categories":[],"explanation":"short"}`, schema, "/explanation"},
		{"builtin before schema", `{"verdict":"bad","categories":[],"explanation":"short"}`, schema, "verdict"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStub(t, reply(c.body))
			j, _ := run(t, s, c.schema, req("x"))
			require.Equal(t, job.RunFailed, j.Finalization.Status)
			require.Equal(t, job.CodeInvalidOutput, j.Finalization.Error.Code)
			require.Contains(t, j.Finalization.Error.Message, c.member)
			require.Nil(t, j.Finalization.Result)
			require.Equal(t, job.StatusCompleted, j.Status)
			for _, r := range j.Analyzers {
				require.Equal(t, job.RunSucceeded, r.Status)
				require.NotNil(t, r.Result)
			}
		})
	}
}

func TestInsufficientEvidenceShortCircuit(t *testing.T) {
	s := newStub(t, reply(okBody))
	e := newEngine(t, an("bad1", "fail")+an("bad2", "fail")+an("ok1", "ok")+finYAML(""), s,
		map[string]adapter.Handler{"fail": failHandler, "ok": okHandler}, engine.Config{})
	// Selecting nothing skips every analyzer: no success, no failure.
	j, sub := create(t, e, engine.CreateRequest{Input: job.Input{Content: "x"}, Analyzers: []string{}})
	evs := drain(t, sub)
	got, _ := e.Get(j.ID)
	require.Zero(t, s.calls.Load())
	require.Equal(t, job.RunSkipped, got.Analyzers[0].Status)
	require.Equal(t, job.StatusCompleted, got.Finalization.Status)
	require.JSONEq(t, `{"verdict":"insufficient_evidence","overall_score":null,"categories":[],"explanation":"No analyzer produced a usable result; no assessment score can be given.","evidence":[],"limitations":[{"kind":"missing_evidence","message":"No analyzer succeeded, so there is no evidence to assess."}],"produced_by":"orchestrator"}`, string(got.Finalization.Result))
	require.Equal(t, 1, count(evs, "analysis.finalizing"))
	require.Equal(t, 1, count(evs, "analysis.finalized"))
	require.Less(t, idx(evs, "analysis.finalized"), idx(evs, "job.completed"))

	// Every analyzer fails: one failed_analyzer limitation each.
	e2 := newEngine(t, an("bad1", "fail")+an("bad2", "fail")+finYAML(""), s, map[string]adapter.Handler{"fail": failHandler}, engine.Config{})
	got = runsOf(t, e2, req("x"))
	require.Zero(t, s.calls.Load())
	var a job.FinalAssessment
	require.NoError(t, json.Unmarshal(got.Finalization.Result, &a))
	require.Equal(t, "orchestrator", a.ProducedBy)
	require.Nil(t, a.OverallScore)
	require.Len(t, a.Limitations, 3)
	require.Equal(t, "missing_evidence", a.Limitations[0].Kind)
	require.Equal(t, []string{"bad1"}, a.Limitations[1].AnalyzerIDs)
	require.Equal(t, "failed_analyzer", a.Limitations[2].Kind)
	require.Equal(t, job.StatusCompleted, got.Status)
}

func TestFailedAndSkippedAnalyzersDoNotPreventFinalization(t *testing.T) {
	s := newStub(t, reply(okBody))
	e := newEngine(t, an("bad", "fail")+an("ok1", "ok")+finYAML(""), s,
		map[string]adapter.Handler{"fail": failHandler, "ok": okHandler}, engine.Config{})
	j := runsOf(t, e, engine.CreateRequest{Input: job.Input{Content: "x"}, Analyzers: []string{"ok1"}})
	require.EqualValues(t, 1, s.calls.Load())
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	j = runsOf(t, e, req("x"))
	require.EqualValues(t, 2, s.calls.Load())
	require.Equal(t, job.RunFailed, j.Analyzers[0].Status)
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
}

func TestCancelBeforeBarrier(t *testing.T) {
	s := newStub(t, reply(okBody))
	release := make(chan struct{})
	e := newEngine(t, an("slow", "slow")+finYAML(""), s, map[string]adapter.Handler{"slow": hold(release)}, engine.Config{})
	j, sub := create(t, e, req("x"))
	_, err := e.Cancel(j.ID)
	require.NoError(t, err)
	evs := drain(t, sub)
	got, _ := e.Get(j.ID)
	require.Equal(t, job.StatusCancelled, got.Status)
	require.Equal(t, job.RunSkipped, got.Finalization.Status)
	require.Equal(t, job.ReasonCancelled, *got.Finalization.Reason)
	require.Zero(t, s.calls.Load())
	require.Zero(t, count(evs, "analysis.ready_for_finalization")+count(evs, "analysis.finalizing")+count(evs, "job.completed"))
}

func TestCancelDuringFinalization(t *testing.T) {
	s := newStub(t, blockUntilGone)
	e := newEngine(t, an("ok1", "ok")+finYAML(""), s, map[string]adapter.Handler{"ok": okHandler}, engine.Config{})
	j, sub := create(t, e, req("x"))
	select {
	case <-s.started:
	case <-time.After(5 * time.Second):
		t.Fatal("finalizer not called")
	}
	_, err := e.Cancel(j.ID)
	require.NoError(t, err)
	evs := drain(t, sub)
	got, _ := e.Get(j.ID)
	require.Equal(t, job.StatusCancelled, got.Status)
	require.Equal(t, job.RunFailed, got.Finalization.Status)
	require.Equal(t, job.CodeCancelled, got.Finalization.Error.Code)
	require.Equal(t, 1, count(evs, "analysis.finalization_failed"))
	require.Less(t, idx(evs, "analysis.finalization_failed"), idx(evs, "job.cancelled"))
	require.Zero(t, count(evs, "job.completed"))
	require.Equal(t, job.RunSucceeded, got.Analyzers[0].Status)
}

func TestFinalizeFalse(t *testing.T) {
	s := newStub(t, reply(okBody))
	f := false
	j, evs := run(t, s, "", engine.CreateRequest{Input: job.Input{Content: "x"}, Finalize: &f})
	require.Zero(t, s.calls.Load())
	require.Equal(t, job.RunSkipped, j.Finalization.Status)
	require.Equal(t, job.ReasonDisabledByRequest, *j.Finalization.Reason)
	require.Equal(t, job.StatusCompleted, j.Status)
	require.Zero(t, count(evs, "analysis.finalizing"))
	require.Equal(t, 1, count(evs, "analysis.ready_for_finalization"))
}

func TestNotConfigured(t *testing.T) {
	hs := handlers(map[string]adapter.Handler{"ok": okHandler})
	for name, y := range map[string]string{
		"absent":   an("ok1", "ok"),
		"disabled": an("ok1", "ok") + finYAML("    enabled: false\n"),
	} {
		t.Run(name, func(t *testing.T) {
			reg := load(t, y, "http://unused", hs)
			exec := &adapter.Executor{Handlers: hs}
			fin := New(reg, exec, nil)
			require.True(t, fin == nil, "must be a true nil interface")
			e := engine.New(reg, engine.Config{Executor: exec, Finalizer: fin})
			defer e.Close()
			j := runsOf(t, e, req("x"))
			require.Equal(t, job.RunSkipped, j.Finalization.Status)
			require.Equal(t, job.ReasonNotConfigured, *j.Finalization.Reason)
			require.Equal(t, job.StatusCompleted, j.Status)
		})
	}
}

func TestSlowAnalyzerDelaysOnlyFinalization(t *testing.T) {
	s := newStub(t, reply(okBody))
	release := make(chan struct{})
	e := newEngine(t, an("fast", "ok")+an("slow", "slow")+finYAML(""), s,
		map[string]adapter.Handler{"ok": okHandler, "slow": hold(release)}, engine.Config{})
	j, sub := create(t, e, req("x"))
	var seen []events.Event
	for !hasType(seen, "analyzer.succeeded") {
		select {
		case ev := <-sub.Events():
			seen = append(seen, ev)
		case <-time.After(5 * time.Second):
			t.Fatal("fast analyzer never settled")
		}
	}
	require.Zero(t, s.calls.Load(), "finalizer must wait for the slow analyzer")
	require.False(t, hasType(seen, "analysis.ready_for_finalization"))
	g, _ := e.Get(j.ID)
	require.Equal(t, job.StatusRunning, g.Status)
	close(release)
	evs := append(seen, drain(t, sub)...)
	require.Less(t, idx(evs, "analyzer.succeeded"), idx(evs, "analysis.ready_for_finalization"))
	require.EqualValues(t, 1, s.calls.Load())
	require.Equal(t, 2, count(evs, "analyzer.succeeded"))
}

func hasType(evs []events.Event, typ string) bool { return idx(evs, typ) >= 0 }

func TestExactlyOnceUnderConcurrentSettles(t *testing.T) {
	const analyzers, rounds = 50, 100
	var y strings.Builder
	for i := 0; i < analyzers; i++ {
		y.WriteString(an(fmt.Sprintf("a%02d", i), "slow"))
	}
	y.WriteString(finYAML(""))
	for r := 0; r < rounds; r++ {
		s := newStub(t, reply(okBody))
		release := make(chan struct{})
		e := newEngine(t, y.String(), s, map[string]adapter.Handler{"slow": hold(release)},
			engine.Config{MaxParallelAnalyzers: analyzers})
		_, sub := create(t, e, req("x"))
		time.Sleep(time.Millisecond)
		close(release) // all analyzers settle at once
		evs := drain(t, sub)
		require.EqualValues(t, 1, s.calls.Load(), "round %d", r)
		require.Equal(t, 1, count(evs, "analysis.ready_for_finalization"), "round %d", r)
		require.Equal(t, 1, count(evs, "analysis.finalizing"), "round %d", r)
		require.Equal(t, 1, count(evs, "analysis.finalized"), "round %d", r)
		require.Equal(t, 1, count(evs, "job.completed"), "round %d", r)
		e.Close()
		s.Close()
	}
}

// The shipped example entry loads, defaults and works end to end.
func TestExampleRegistryEntry(t *testing.T) {
	s := newStub(t, reply(okBody))
	dir := t.TempDir()
	ex, err := os.ReadFile("../../docs/examples/final_synthesis.yaml")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "final.yaml"), ex, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "an.yaml"), []byte("analyzers:\n"+an("ok1", "ok")), 0o600))
	hs := handlers(map[string]adapter.Handler{"ok": okHandler})
	reg, err := registry.Load(dir, registry.Options{HandlerExists: hs.Has,
		Getenv: func(k string) (string, bool) { return s.URL, k == "FINALIZER_URL" }})
	require.NoError(t, err)
	require.Equal(t, 60*time.Second, reg.Finalizer().Execution.Timeout)
	exec := &adapter.Executor{Handlers: hs}
	e := engine.New(reg, engine.Config{Executor: exec, Finalizer: New(reg, exec, nil)})
	defer e.Close()
	j := runsOf(t, e, req("hello"))
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	require.EqualValues(t, 1, s.calls.Load())
}

func TestErrorLogHasOnlyJobIDAndCode(t *testing.T) {
	var buf strings.Builder
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "SECRET-BODY", 500) })
	hs := handlers(map[string]adapter.Handler{"ok": okHandler})
	reg := load(t, an("ok1", "ok")+finYAML(""), s.URL, hs)
	exec := &adapter.Executor{Handlers: hs}
	e := engine.New(reg, engine.Config{Executor: exec, Finalizer: New(reg, exec, slog.New(slog.NewTextHandler(&buf, nil)))})
	defer e.Close()
	j := runsOf(t, e, req("x"))
	out := buf.String()
	require.Contains(t, out, "job_id="+j.ID)
	require.Contains(t, out, "code=adapter_error")
	require.NotContains(t, out, "SECRET-BODY")
	require.NotContains(t, out, s.URL)
}
