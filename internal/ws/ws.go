// Package ws serves GET /api/v1/jobs/{id}/events (analysis-job-events).
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/httpapi"
)

// Close codes besides 1000 (normal) and 1001 (going away).
const CloseSlowConsumer websocket.StatusCode = 4001

// Source is the engine part the endpoint needs; *engine.Engine implements it.
// The subscription buffer (256 events) is the engine's EventBuffer.
type Source interface {
	Subscribe(id string) (*events.Subscription, error)
}

type Options struct {
	PingInterval time.Duration // default 25s
	WriteTimeout time.Duration // default 10s
	Logger       *slog.Logger  // default slog.Default()
}

// Handler is the event stream endpoint.
type Handler struct {
	src Source
	opt Options

	mu      sync.Mutex
	closing bool
	done    chan struct{}
	wg      sync.WaitGroup
}

func New(src Source, opt Options) *Handler {
	if opt.PingInterval <= 0 {
		opt.PingInterval = 25 * time.Second
	}
	if opt.WriteTimeout <= 0 {
		opt.WriteTimeout = 10 * time.Second
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	return &Handler{src: src, opt: opt, done: make(chan struct{})}
}

// Shutdown refuses new connections, closes open ones with 1001 and waits for
// them (until ctx ends). http.Server.Shutdown does not track hijacked connections.
func (h *Handler) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	if !h.closing {
		h.closing = true
		close(h.done)
	}
	h.mu.Unlock()
	finished := make(chan struct{})
	go func() { h.wg.Wait(); close(finished) }()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_request", "this endpoint requires a WebSocket upgrade", nil)
		return
	}
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		httpapi.WriteError(w, http.StatusServiceUnavailable, "unavailable", "server is shutting down", nil)
		return
	}
	h.wg.Add(1)
	h.mu.Unlock()
	defer h.wg.Done()

	sub, err := h.src.Subscribe(id) // unknown job: 404 before any upgrade
	switch {
	case errors.Is(err, engine.ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "job not found", nil)
		return
	case err != nil:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "internal error", nil)
		return
	}
	defer sub.Close()
	// Origin patterns "*": the API has no cookie or ambient credentials, so a
	// cross-origin page gains nothing; restricting origins is the gateway's job.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return // Accept already answered
	}
	code := h.serve(conn, sub)
	h.opt.Logger.Info("event stream closed", "job_id", id, "close_code", int(code))
}

// serve forwards events until the stream ends and returns the close code.
func (h *Handler) serve(conn *websocket.Conn, sub *events.Subscription) websocket.StatusCode {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	conn.SetReadLimit(-1) // client messages of any size are ignored, streamed to io.Discard (never buffered)
	go func() {           // Reader also answers pings and the close handshake
		defer stop()
		for {
			_, r, err := conn.Reader(ctx)
			if err != nil {
				return
			}
			if _, err := io.Copy(io.Discard, r); err != nil {
				return
			}
		}
	}()
	ping := time.NewTicker(h.opt.PingInterval)
	defer ping.Stop()
	for {
		select {
		case ev, ok := <-sub.Events():
			if !ok {
				if errors.Is(sub.Err(), events.ErrSlowConsumer) {
					_ = conn.Close(CloseSlowConsumer, "slow_consumer")
					return CloseSlowConsumer
				}
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return websocket.StatusNormalClosure
			}
			b, err := json.Marshal(ev)
			if err != nil {
				_ = conn.Close(websocket.StatusInternalError, "internal error")
				return websocket.StatusInternalError
			}
			wctx, cancel := context.WithTimeout(ctx, h.opt.WriteTimeout)
			err = conn.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				_ = conn.CloseNow()
				return websocket.StatusAbnormalClosure
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, h.opt.WriteTimeout)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				_ = conn.CloseNow()
				return websocket.StatusAbnormalClosure
			}
		case <-h.done:
			_ = conn.Close(websocket.StatusGoingAway, "server shutting down")
			return websocket.StatusGoingAway
		case <-ctx.Done():
			return websocket.StatusAbnormalClosure // client left
		}
	}
}
