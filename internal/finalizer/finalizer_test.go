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

const okBody = `{"verdict":"detected","confidence":0.86,"user_text":"because","reasoning":{"b":2},"usage":{"total_tokens":1},"produced_by":"orchestrator"}`

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
	fin := New(reg, exec, nil)
	require.NotNil(t, fin)
	cfg.Finalizer = gatedFinalizer{fin}
	cfg.MaxParallelJobs = 1 // required by create(), see gate_test.go
	e := engine.New(reg, cfg)
	t.Cleanup(e.Close)
	return e
}

// create starts a job and subscribes to it before it can emit anything: the
// subscription sees job.snapshot, job.started and every later event. e must
// come from newEngine.
func create(t *testing.T, e *engine.Engine, req engine.CreateRequest) (job.Job, *events.Subscription) {
	t.Helper()
	gate, err := e.Create(engine.CreateRequest{Input: job.Input{Content: gateContent}, Analyzers: []string{}})
	require.NoError(t, err)
	defer close(gateCh(gate.ID)) // also on failure, so e.Close can finish
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
	created, sub := create(t, e, r)
	evs := drain(t, sub)
	j, err := e.Get(created.ID)
	require.NoError(t, err)
	return j, evs
}

func TestSuccessNormalizesAndDropsUnknownMembers(t *testing.T) {
	s := newStub(t, reply(okBody))
	j, evs := run(t, s, "", req("Hello  world"))
	require.Equal(t, job.StatusCompleted, j.Status)
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	require.EqualValues(t, 1, s.calls.Load())
	// extra members dropped; usage only from the adapter (none for http_sync)
	require.JSONEq(t, `{"verdict":"detected","confidence":0.86,"user_text":"because","produced_by":"finalizer","usage":null,"attempts":null,"retry_reason":null,"wikipedia_quotes":[]}`, string(j.Finalization.Result))
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
	s := newStub(t, reply(`{"verdict":"uncertain","user_text":"The analyzers disagree."}`))
	j, _ := run(t, s, "", req("x"))
	require.JSONEq(t, `{"verdict":"uncertain","confidence":null,"user_text":"The analyzers disagree.","produced_by":"finalizer","usage":null,"attempts":null,"retry_reason":null,"wikipedia_quotes":[]}`, string(j.Finalization.Result))
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
	schema := "    output:\n      schema: {type: object, properties: {user_text: {minLength: 10}}}\n"
	cases := []struct {
		name, body, schema, member string
	}{
		{"missing user_text", `{"verdict":"detected"}`, "", "user_text"},
		{"missing verdict", `{"user_text":"e"}`, "", "verdict"},
		{"old shape", `{"verdict":"insufficient_evidence","overall_score":40,"categories":[],"explanation":"..."}`, "", "verdict"},
		{"user_text null", `{"verdict":"detected","user_text":null}`, "", "user_text"},
		{"user_text blank", `{"verdict":"detected","user_text":"  "}`, "", "user_text"},
		{"user_text not string", `{"verdict":"detected","user_text":3}`, "", "user_text"},
		{"confidence too high", `{"verdict":"detected","confidence":86,"user_text":"e"}`, "", "confidence"},
		{"confidence negative", `{"verdict":"detected","confidence":-0.1,"user_text":"e"}`, "", "confidence"},
		{"confidence not number", `{"verdict":"detected","confidence":"0.9","user_text":"e"}`, "", "confidence"},
		{"schema violation", `{"verdict":"detected","user_text":"short"}`, schema, "/user_text"},
		{"builtin before schema", `{"verdict":"bad","user_text":"short"}`, schema, "verdict"},
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

const shortCircuit = `{"verdict":"uncertain","confidence":null,"user_text":"No analyzer produced a usable result, so no verdict can be given.","produced_by":"orchestrator","usage":null,"attempts":null,"retry_reason":null,"wikipedia_quotes":[]}`

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
	require.JSONEq(t, shortCircuit, string(got.Finalization.Result))
	require.Equal(t, 1, count(evs, "analysis.finalizing"))
	require.Equal(t, 1, count(evs, "analysis.finalized"))
	require.Less(t, idx(evs, "analysis.finalized"), idx(evs, "job.completed"))

	// Every analyzer fails.
	e2 := newEngine(t, an("bad1", "fail")+an("bad2", "fail")+finYAML(""), s, map[string]adapter.Handler{"fail": failHandler}, engine.Config{})
	got = runsOf(t, e2, req("x"))
	require.Zero(t, s.calls.Load())
	require.JSONEq(t, shortCircuit, string(got.Finalization.Result))
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
			// No event assertions here and no finalizer to gate on, so no create().
			j, err := e.Create(req("x"))
			require.NoError(t, err)
			j = waitDone(t, e, j.ID)
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
	require.Equal(t, 2, count(evs, "analyzer.succeeded"))
	require.Less(t, lastIdx(evs, "analyzer.succeeded"), idx(evs, "analysis.ready_for_finalization"))
	require.EqualValues(t, 1, s.calls.Load())
}

func lastIdx(evs []events.Event, typ string) (n int) {
	n = -1
	for i, t := range types(evs) {
		if t == typ {
			n = i
		}
	}
	return
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

func TestErrorLogHasOnlyJobIDAndCode(t *testing.T) {
	var buf strings.Builder
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "SECRET-BODY", 500) })
	hs := handlers(map[string]adapter.Handler{"ok": okHandler})
	reg := load(t, an("ok1", "ok")+finYAML(""), s.URL, hs)
	exec := &adapter.Executor{Handlers: hs}
	e := engine.New(reg, engine.Config{Executor: exec, Finalizer: gatedFinalizer{New(reg, exec, slog.New(slog.NewTextHandler(&buf, nil)))}, MaxParallelJobs: 1})
	defer e.Close()
	j := runsOf(t, e, req("x"))
	out := buf.String()
	require.Contains(t, out, "job_id="+j.ID)
	require.Contains(t, out, "code=adapter_error")
	require.NotContains(t, out, "SECRET-BODY")
	require.NotContains(t, out, s.URL)
}
