package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// an renders one custom analyzer definition.
func an(id, handler string, extra ...string) string {
	return fmt.Sprintf("  - id: %s\n    execution: {adapter: custom, handler: %s}\n%s", id, handler, strings.Join(extra, ""))
}

func deps(ids ...string) string { return "    depends_on: [" + strings.Join(ids, ",") + "]\n" }

func regYAML(analyzers ...string) string { return "analyzers:\n" + strings.Join(analyzers, "") }

// newEngine builds an engine from registry YAML and custom handlers. When
// hold is true, jobs wait until release() so a test can subscribe first.
func newEngine(t *testing.T, yaml string, cfg Config, handlers map[string]adapter.Handler) *Engine {
	t.Helper()
	h := adapter.NewHandlers()
	for n, fn := range handlers {
		h.Register(n, fn)
	}
	path := filepath.Join(t.TempDir(), "reg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	reg, err := registry.Load(path, registry.Options{HandlerExists: h.Has})
	require.NoError(t, err)
	cfg.Executor = &adapter.Executor{Handlers: h}
	e := New(reg, cfg)
	t.Cleanup(e.Close)
	return e
}

func ok(_ context.Context, req adapter.Request) (any, error) {
	return map[string]string{"by": req.AnalyzerID}, nil
}

func block(ctx context.Context, _ adapter.Request) (any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func in(content string) CreateRequest { return CreateRequest{Input: job.Input{Content: content}} }

// createWithEvents creates a job and subscribes before it starts, so the
// subscription sees every event from seq 1.
func createWithEvents(t *testing.T, e *Engine, req CreateRequest) (job.Job, *events.Subscription) {
	t.Helper()
	e.gate = make(chan struct{})
	j, err := e.Create(req)
	require.NoError(t, err)
	sub, err := e.Subscribe(j.ID)
	require.NoError(t, err)
	close(e.gate)
	return j, sub
}

// drain reads until the stream ends.
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
			t.Fatalf("event stream did not end; got %v", types(out))
		}
	}
}

func types(evs []events.Event) []string {
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

// typesFor returns "type[:analyzer id]" entries.
func typesFor(evs []events.Event) []string {
	var out []string
	for _, ev := range evs {
		s := ev.Type
		if m, ok := ev.Data.(map[string]any); ok {
			if a, ok := m["analyzer"].(job.AnalyzerRun); ok {
				s += ":" + a.ID
			}
		}
		out = append(out, s)
	}
	return out
}

func count(evs []events.Event, typ string) (n int) {
	for _, ev := range evs {
		if ev.Type == typ {
			n++
		}
	}
	return
}

func index(ss []string, s string) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return -1
}

func requireSeq(t *testing.T, evs []events.Event) {
	t.Helper()
	require.Equal(t, "job.snapshot", evs[0].Type)
	require.EqualValues(t, 0, evs[0].Seq)
	for i, ev := range evs[1:] {
		require.EqualValues(t, i+1, ev.Seq, "seq gap at %s", ev.Type)
	}
}

func waitDone(t *testing.T, e *Engine, id string) job.Job {
	t.Helper()
	var j job.Job
	require.Eventually(t, func() bool {
		var err error
		j, err = e.Get(id)
		return err == nil && j.Done
	}, 10*time.Second, 2*time.Millisecond)
	return j
}

func ar(j job.Job, id string) job.AnalyzerRun {
	for _, r := range j.Analyzers {
		if r.ID == id {
			return r
		}
	}
	panic("no analyzer " + id)
}

// fakeFinalizer implements Finalizer.
type fakeFinalizer struct {
	mu    sync.Mutex
	calls int
	in    FinalizationInput
	fn    func(ctx context.Context) (json.RawMessage, error)
}

func (f *fakeFinalizer) Run(ctx context.Context, in FinalizationInput) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls++
	f.in = in
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(ctx)
	}
	return json.RawMessage(`{"verdict":"low_risk","categories":[],"explanation":"ok"}`), nil
}

func (f *fakeFinalizer) Calls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

const finDef = `  - id: final_synthesis
    trigger: after_all_analyzers_settled
    execution: {adapter: custom, handler: final, timeout: 1s}
    input:
      bindings: {content: $input, results: $job.analyzers, coverage: $job.coverage}
`

type rawMsg = json.RawMessage

var eventsErrSlow = events.ErrSlowConsumer
