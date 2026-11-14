// Command scraper serves the shared social-scraper-api (GET /health, POST /scrape).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stderr)
	stop()
	os.Exit(code)
}

// run is main without the process: it serves until ctx ends, then shuts down within 10 s. It returns the exit code.
func run(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) int {
	fs := flag.NewFlagSet("scraper", flag.ContinueOnError)
	fs.SetOutput(stderr)
	healthcheck := fs.Bool("healthcheck", false, "request /health of a running scraper and exit 0 on 200, else 1")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *healthcheck {
		return runHealthcheck(getenv("SCRAPER_LISTEN_ADDR"))
	}
	cfg, err := ss.LoadConfig(getenv)
	if err != nil {
		fmt.Fprintln(stderr, "scraper:", err)
		return 1
	}
	log := ss.NewLogger(stderr, ss.NewRedactor(getenv, os.ReadFile))
	registerPlatforms(cfg)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           ss.NewServer(cfg, ss.Default, log),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("scraper listening", "addr", cfg.ListenAddr)
	select {
	case err := <-errc:
		log.Error("scraper stopped", "error", err.Error())
		return 1
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("shutdown", "error", err.Error())
		return 1
	}
	return 0
}

// runHealthcheck requests http://127.0.0.1:<port>/health, the port taken from listenAddr (default 8003).
func runHealthcheck(listenAddr string) int {
	port := "8003"
	if listenAddr != "" {
		if _, p, err := net.SplitHostPort(listenAddr); err == nil && p != "" {
			port = p
		}
	}
	c := http.Client{Timeout: 4 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
