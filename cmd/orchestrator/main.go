// Command orchestrator serves the analysis orchestrator HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/finalizer"
	"github.com/junction-2026/backend/internal/httpapi"
	"github.com/junction-2026/backend/internal/registry"
	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/store"
	"github.com/junction-2026/backend/internal/ws"
)

const shutdownTimeout = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := run(ctx, os.LookupEnv, os.Stdout, nil); err != nil {
		fmt.Fprintln(os.Stderr, "orchestrator:", err)
		os.Exit(1)
	}
}

// run serves until ctx ends, then shuts down within shutdownTimeout. ready, if
// set, receives the listen address once the server accepts connections.
func run(ctx context.Context, lookup func(string) (string, bool), logOut io.Writer, ready func(addr string)) error {
	getenv := func(k string) string { v, _ := lookup(k); return v }
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	// Bearer values and secret scraper variables never reach a log line.
	redactor := ss.NewRedactor(getenv)
	log := ss.NewLogger(logOut, redactor)
	for _, name := range cfg.Ignored {
		log.Warn("ignoring obsolete scraper variable: scraping runs in process", "variable", name)
	}

	handlers := adapter.NewHandlers()
	reg, err := registry.Load(cfg.AnalyzersConfig, registry.Options{Getenv: lookup, HandlerExists: handlers.Has})
	if err != nil {
		return fmt.Errorf("loading analyzers from %s: %w", cfg.AnalyzersConfig, err)
	}

	ecfg := cfg.Engine
	ecfg.Executor = &adapter.Executor{Handlers: handlers}
	// The only place that sets ecfg.Finalizer: nil (no enabled finalizer) means
	// jobs finish with finalization skipped / not_configured. The registry has
	// already checked an enabled llm finalizer's settings and read its prompt.
	ecfg.Finalizer = finalizer.New(reg, ecfg.Executor, log)
	if cfg.ScraperEnabled { // nil Scraper: jobs with scrape answer 503 scrape_unavailable
		ecfg.Scraper = newScraper(cfg.Upstream, log, redactor)
	}
	if cfg.JobStore == "sqlite" {
		st, err := store.OpenSQLite(cfg.JobStorePath)
		if err != nil {
			return fmt.Errorf("JOB_STORE_PATH: %w", err)
		}
		defer st.Close() // after eng.Close (deferred later): no job writes after the database closed
		ecfg.Store = st
	}
	ecfg.Warn = func(msg string, args ...any) { log.Warn(msg, args...) }
	eng := engine.New(reg, ecfg)
	defer eng.Close()
	// Jobs that were not done when the server stopped end failed before any request is served.
	n, err := eng.Recover()
	if err != nil {
		return fmt.Errorf("recovering jobs: %w", err)
	}
	if n > 0 {
		log.Info("interrupted jobs ended", "count", n)
	}

	events := ws.New(eng, ws.Options{Logger: log})
	srv := &http.Server{
		Handler:           httpapi.NewHandler(httpapi.Config{Service: eng, Registry: reg, Events: events, Logger: log}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.ListenAddr, err)
	}
	log.Info("listening", "addr", ln.Addr().String(), "analyzers", len(reg.All()))
	if ready != nil {
		ready(ln.Addr().String())
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	// WebSocket first: it refuses new upgrades and closes open ones with 1001
	// (http.Server.Shutdown ignores hijacked connections). Engine.Close, deferred,
	// then aborts running jobs.
	if err := events.Shutdown(sctx); err != nil {
		log.Warn("websocket connections did not close in time")
	}
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		_ = srv.Close()
	}
	return nil
}
