package ws_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/httpapi"
	"github.com/junction-2026/backend/internal/registry"
	"github.com/junction-2026/backend/internal/ws"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// ---- fixtures --------------------------------------------------------------

type stack struct {
	srv   *httptest.Server
	eng   *engine.Engine
	gates map[string]chan struct{}
}

// newStack runs the real engine, REST API and event stream behind httptest.
// Analyzers named in gated block until release(id).
func newStack(t *testing.T, yaml string, gated ...string) *stack {
	t.Helper()
	s := &stack{gates: map[string]chan struct{}{}}
	h := adapter.NewHandlers()
	for _, id := range gated {
		gate := make(chan struct{})
		s.gates[id] = gate
		h.Register(id, func(ctx context.Context, req adapter.Request) (any, error) {
			select {
			case <-gate:
				return map[string]string{"by": req.AnalyzerID}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
	}
	h.Register("instant", func(_ context.Context, req adapter.Request) (any, error) {
		return map[string]string{"by": req.AnalyzerID}, nil
	})
	h.Register("fail", func(context.Context, adapter.Request) (any, error) { return nil, errors.New("boom") })
	p := filepath.Join(t.TempDir(), "r.yaml")
	require.NoError(t, os.WriteFile(p, []byte(yaml), 0o600))
	reg, err := registry.Load(p, registry.Options{HandlerExists: h.Has})
	require.NoError(t, err)
	s.eng = engine.New(reg, engine.Config{Executor: nothingExec{&adapter.Executor{Handlers: h}}})
	t.Cleanup(s.eng.Close)
	wsh := ws.New(s.eng, ws.Options{Logger: quiet})
	s.srv = httptest.NewServer(httpapi.NewHandler(httpapi.Config{Service: s.eng, Registry: reg, Events: wsh, Logger: quiet}))
	t.Cleanup(s.srv.Close)
	return s
}

// nothingExec makes analyzer "ns" skip at run time like news_search with no topics.
type nothingExec struct{ engine.Executor }

func (x nothingExec) Execute(ctx context.Context, def *registry.Analyzer, req adapter.Request) (json.RawMessage, error) {
	if def.ID == "ns" {
		return nil, &adapter.Skip{Reason: adapter.ReasonNoSearchableTopics}
	}
	return x.Executor.Execute(ctx, def, req)
}

func an(id, handler, extra string) string {
	return fmt.Sprintf("  - id: %s\n    execution: {adapter: custom, handler: %s}\n%s", id, handler, extra)
}

func (s *stack) release(id string) { close(s.gates[id]) }

func (s *stack) create(t *testing.T) string {
	t.Helper()
	res, err := http.Post(s.srv.URL+"/api/v1/jobs", "application/json", strings.NewReader(`{"input":{"content":"hello"}}`))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 201, res.StatusCode)
	var j struct{ ID string }
	require.NoError(t, json.NewDecoder(res.Body).Decode(&j))
	return j.ID
}

func (s *stack) rest(t *testing.T, id string) map[string]any {
	t.Helper()
	res, err := http.Get(s.srv.URL + "/api/v1/jobs/" + id)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
	var j map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&j))
	return j
}

func run(j map[string]any, id string) map[string]any {
	for _, a := range j["analyzers"].([]any) {
		if a.(map[string]any)["id"] == id {
			return a.(map[string]any)
		}
	}
	return nil
}

// waitREST polls GET /jobs/{id} until cond holds.
func (s *stack) waitREST(t *testing.T, id string, cond func(map[string]any) bool) {
	t.Helper()
	require.Eventually(t, func() bool { return cond(s.rest(t, id)) }, 5*time.Second, 5*time.Millisecond)
}

func wsURL(base, id string) string {
	return "ws" + strings.TrimPrefix(base, "http") + "/api/v1/jobs/" + id + "/events"
}

func dial(t *testing.T, base, id string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(base, id), nil)
	require.NoError(t, err)
	c.SetReadLimit(1 << 20)
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func read(c *websocket.Conn) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	typ, b, err := c.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, errors.New("not a text frame")
	}
	var ev map[string]any
	return ev, json.Unmarshal(b, &ev)
}

func mustRead(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ev, err := read(c)
	require.NoError(t, err)
	return ev
}

func data(ev map[string]any) map[string]any { return ev["data"].(map[string]any) }

// wantClosed asserts the next read fails with the close code (and reason).
func wantClosed(t *testing.T, c *websocket.Conn, code websocket.StatusCode, reason string) {
	t.Helper()
	_, err := read(c)
	var ce websocket.CloseError
	require.ErrorAs(t, err, &ce)
	require.Equal(t, code, ce.Code)
	require.Equal(t, reason, ce.Reason)
}

// ---- tests on the real engine -----------------------------------------------

func TestUnknownJobIs404BeforeUpgrade(t *testing.T) {
	s := newStack(t, "analyzers:\n"+an("x", "instant", ""))
	_, res, err := websocket.Dial(context.Background(), wsURL(s.srv.URL, "does-not-exist"), nil)
	require.Error(t, err)
	require.Equal(t, 404, res.StatusCode)
	var e struct{ Error struct{ Code string } }
	require.NoError(t, json.NewDecoder(res.Body).Decode(&e))
	require.Equal(t, "not_found", e.Error.Code)
}

func TestPlainRequestIsRejectedWithEnvelope(t *testing.T) {
	s := newStack(t, "analyzers:\n"+an("x", "instant", ""))
	id := s.create(t)
	res, err := http.Get(s.srv.URL + "/api/v1/jobs/" + id + "/events")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 400, res.StatusCode)
	var e struct{ Error struct{ Code string } }
	require.NoError(t, json.NewDecoder(res.Body).Decode(&e))
	require.Equal(t, "invalid_request", e.Error.Code)
}

func TestLateSubscriber(t *testing.T) {
	s := newStack(t, "analyzers:\n"+an("x", "instant", "")+an("y", "y", ""), "y")
	id := s.create(t)
	s.waitREST(t, id, func(j map[string]any) bool {
		return run(j, "x")["status"] == "succeeded" && run(j, "y")["status"] == "running"
	})

	c := dial(t, s.srv.URL, id)
	snap := mustRead(t, c)
	require.Equal(t, "job.snapshot", snap["type"])
	require.Equal(t, id, snap["job_id"])
	job := data(snap)["job"].(map[string]any)
	require.Equal(t, "succeeded", run(job, "x")["status"])
	require.Equal(t, map[string]any{"by": "x"}, run(job, "x")["result"])
	require.Equal(t, "running", run(job, "y")["status"])

	s.release("y")
	var types []string
	last := snap["seq"].(float64)
	for {
		ev, err := read(c)
		if err != nil {
			var ce websocket.CloseError
			require.ErrorAs(t, err, &ce)
			require.Equal(t, websocket.StatusNormalClosure, ce.Code)
			break
		}
		require.Equal(t, last+1, ev["seq"], "no gaps, no replay of what the snapshot contained")
		last = ev["seq"].(float64)
		types = append(types, ev["type"].(string))
	}
	require.Equal(t, []string{"analyzer.succeeded", "analysis.ready_for_finalization", "job.completed"}, types)
}

func TestFinishedJobGetsOnlySnapshotThenClose(t *testing.T) {
	s := newStack(t, "analyzers:\n"+an("x", "instant", ""))
	id := s.create(t)
	s.waitREST(t, id, func(j map[string]any) bool { return j["done"] == true })

	c := dial(t, s.srv.URL, id)
	snap := mustRead(t, c)
	require.Equal(t, "job.snapshot", snap["type"])
	require.Equal(t, true, data(snap)["job"].(map[string]any)["done"])
	require.Positive(t, snap["seq"])
	wantClosed(t, c, websocket.StatusNormalClosure, "")
}

// TestEventsMatchREST reads GET /jobs/{id} after every event and compares the
// entity in the event with the REST state (analysis-job-events: Event and REST
// consistency). Every analyzer is gated so the job is quiescent between steps.
func TestEventsMatchRESTAndAreOrdered(t *testing.T) {
	s := newStack(t, "analyzers:\n"+an("a", "a", "")+an("b", "b", "    depends_on: [a]\n")+an("c", "c", ""), "a", "b", "c")
	id := s.create(t)
	s.waitREST(t, id, func(j map[string]any) bool {
		return run(j, "a")["status"] == "running" && run(j, "c")["status"] == "running"
	})

	c := dial(t, s.srv.URL, id)
	snap := mustRead(t, c)
	require.Equal(t, "job.snapshot", snap["type"])
	require.Equal(t, s.rest(t, id), data(snap)["job"], "snapshot equals REST")
	last := snap["seq"].(float64)

	type step struct {
		release string
		want    []string
	}
	steps := []step{
		{"c", []string{"analyzer.succeeded"}},
		{"a", []string{"analyzer.succeeded", "analyzer.started"}},
		{"b", []string{"analyzer.succeeded", "analysis.ready_for_finalization", "job.completed"}},
	}
	for _, st := range steps {
		s.release(st.release)
		for _, want := range st.want {
			ev := mustRead(t, c)
			require.Equal(t, want, ev["type"])
			require.Equal(t, last+1, ev["seq"])
			last = ev["seq"].(float64)
			require.Equal(t, id, ev["job_id"])
			require.NotEmpty(t, ev["emitted_at"])
			j := s.rest(t, id) // REST after the event
			switch d := data(ev); {
			case d["analyzer"] != nil:
				ea := d["analyzer"].(map[string]any)
				require.Equal(t, run(j, ea["id"].(string)), ea, "%s %v", ev["type"], ea["id"])
			case d["coverage"] != nil:
				require.Equal(t, j["coverage"], d["coverage"])
			case d["job"] != nil:
				require.Equal(t, j, d["job"])
			}
		}
	}
	wantClosed(t, c, websocket.StatusNormalClosure, "")
}

func TestClientMessagesAreIgnored(t *testing.T) {
	s := newStack(t, "analyzers:\n"+an("y", "y", ""), "y")
	id := s.create(t)
	c := dial(t, s.srv.URL, id)
	mustRead(t, c)
	require.NoError(t, c.Write(context.Background(), websocket.MessageText, []byte(`{"cmd":"cancel"}`)))
	require.NoError(t, c.Write(context.Background(), websocket.MessageBinary, []byte{1, 2, 3}))
	require.NoError(t, c.Write(context.Background(), websocket.MessageText, []byte(strings.Repeat("x", 200<<10)))) // > the 32 KiB library default
	s.release("y")
	for _, want := range []string{"analyzer.succeeded", "analysis.ready_for_finalization", "job.completed"} {
		require.Equal(t, want, mustRead(t, c)["type"])
	}
	require.Equal(t, "completed", s.rest(t, id)["status"])
}

// ---- tests on a bare event bus ---------------------------------------------

type busSource struct{ bus *events.Bus }

func (b busSource) Subscribe(id string) (*events.Subscription, error) {
	if id != "j1" {
		return nil, engine.ErrNotFound
	}
	return b.bus.Subscribe(id, func() any { return map[string]any{"job": map[string]any{"id": id}} }), nil
}

func newBusServer(t *testing.T, opt ws.Options) (*httptest.Server, *events.Bus, *ws.Handler) {
	t.Helper()
	bus := events.New(256)
	opt.Logger = quiet
	h := ws.New(busSource{bus}, opt)
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/jobs/{id}/events", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, bus, h
}

func TestSlowConsumerIsClosedWith4001(t *testing.T) {
	srv, bus, _ := newBusServer(t, ws.Options{})
	c := dial(t, srv.URL, "j1") // the server subscribed before the handshake finished

	payload := strings.Repeat("x", 16<<10)
	const total = 6000
	for i := 0; i < total; i++ { // the client reads nothing meanwhile
		bus.Publish("j1", "analyzer.succeeded", map[string]any{"pad": payload})
	}

	got := 0
	last := float64(-1) // the snapshot has seq 0
	for {
		ev, err := read(c)
		if err != nil {
			var ce websocket.CloseError
			require.ErrorAs(t, err, &ce)
			require.Equal(t, ws.CloseSlowConsumer, ce.Code)
			require.Equal(t, "slow_consumer", ce.Reason)
			break
		}
		require.Equal(t, last, ev["seq"].(float64)-1, "events before the close arrive without gaps")
		last = ev["seq"].(float64)
		got++
	}
	require.Positive(t, got)
	require.Less(t, got, total, "the overflowing events are not delivered")

	// a reconnect starts with a fresh snapshot at the current seq
	c2 := dial(t, srv.URL, "j1")
	snap := mustRead(t, c2)
	require.Equal(t, "job.snapshot", snap["type"])
	require.Equal(t, float64(total), snap["seq"])
}

func TestShutdownClosesConnectionsWith1001(t *testing.T) {
	srv, _, h := newBusServer(t, ws.Options{})
	c := dial(t, srv.URL, "j1")
	mustRead(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Shutdown(ctx) }()
	wantClosed(t, c, websocket.StatusGoingAway, "server shutting down")
	require.NoError(t, <-done, "Shutdown returns once the connection is closed")

	_, res, err := websocket.Dial(context.Background(), wsURL(srv.URL, "j1"), nil)
	require.Error(t, err)
	require.Equal(t, 503, res.StatusCode, "no new upgrades during shutdown")
}

func TestServerPingsAndDiscardsClientTraffic(t *testing.T) {
	srv, _, _ := newBusServer(t, ws.Options{PingInterval: 20 * time.Millisecond})
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	require.NoError(t, err)
	defer conn.Close()
	_, err = fmt.Fprint(conn, "GET /api/v1/jobs/j1/events HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	require.NoError(t, err)
	r := bufio.NewReader(conn)
	res, err := http.ReadResponse(r, nil)
	require.NoError(t, err)
	require.Equal(t, 101, res.StatusCode)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	var opcodes []byte
	for len(opcodes) < 3 {
		var hdr [2]byte
		_, err := io.ReadFull(r, hdr[:])
		require.NoError(t, err)
		n := int64(hdr[1] & 0x7f)
		if n == 126 {
			var ext [2]byte
			_, err = io.ReadFull(r, ext[:])
			require.NoError(t, err)
			n = int64(ext[0])<<8 | int64(ext[1])
		}
		payload := make([]byte, n)
		_, err = io.ReadFull(r, payload)
		require.NoError(t, err)
		opcodes = append(opcodes, hdr[0]&0x0f)
		if hdr[0]&0x0f == 9 { // echo the payload in a masked pong (zero mask), or the server waits for it
			_, err = conn.Write(append([]byte{0x8a, 0x80 | byte(n), 0, 0, 0, 0}, payload...))
			require.NoError(t, err)
		}
	}
	require.Equal(t, byte(1), opcodes[0], "snapshot is a text frame")
	require.Equal(t, []byte{9, 9}, opcodes[1:], "then pings")
}

// analyzer-run-availability: REST shows availability/note on every run (null
// while pending or running) and settle events carry the same values.
func TestAvailabilityInRESTAndEvents(t *testing.T) {
	onG := "    depends_on: [g]\n"
	s := newStack(t, "analyzers:\n"+an("g", "g", "")+an("x", "instant", onG)+an("f", "fail", onG)+
		an("df", "instant", "    depends_on: [f]\n")+an("ns", "instant", onG)+an("ds", "instant", "    depends_on: [ns]\n")+
		an("off", "instant", "    enabled: false\n"), "g")
	id := s.create(t)
	s.waitREST(t, id, func(j map[string]any) bool { return run(j, "g")["status"] == "running" })
	j := s.rest(t, id)
	for _, a := range []string{"g", "x", "f"} {
		r := run(j, a)
		require.Contains(t, r, "availability")
		require.Contains(t, r, "note")
		require.Nil(t, r["availability"], a)
		require.Nil(t, r["note"], a)
	}
	require.Equal(t, "not_applicable", run(j, "off")["availability"])

	c := dial(t, s.srv.URL, id)
	require.Equal(t, "job.snapshot", mustRead(t, c)["type"])
	s.release("g")
	var settled []map[string]any
	for {
		ev, err := read(c)
		if err != nil {
			break
		}
		if typ := ev["type"].(string); typ == "analyzer.succeeded" || typ == "analyzer.failed" || typ == "analyzer.skipped" {
			settled = append(settled, data(ev)["analyzer"].(map[string]any))
		}
	}
	require.Len(t, settled, 6)

	fin := s.rest(t, id)
	for _, ev := range settled {
		r := run(fin, ev["id"].(string))
		require.Equal(t, r["availability"], ev["availability"], ev["id"])
		require.Equal(t, r["note"], ev["note"], ev["id"])
	}
	unavailable := "Unavailable: the analyzer failed or is not set up, so its result is missing."
	nothing := "Not run: the text names no concrete topics or events to search news coverage for."
	want := map[string][2]any{"g": {"ran", nil}, "x": {"ran", nil}, "f": {"unavailable", unavailable},
		"df": {"unavailable", unavailable}, "ns": {"not_applicable", nothing}, "ds": {"not_applicable", nothing},
		"off": {"not_applicable", "Not run: this analyzer was not requested for this job."}}
	for a, w := range want {
		require.Equal(t, w[0], run(fin, a)["availability"], a)
		require.Equal(t, w[1], run(fin, a)["note"], a)
	}
	require.Equal(t, "dependency_failed", run(fin, "df")["reason"])
	require.Equal(t, "dependency_skipped", run(fin, "ds")["reason"])
}
