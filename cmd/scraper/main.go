// Command scraper serves the shared social-scraper-api (GET /health, POST /scrape).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "request /health of a running scraper and exit 0 on 200, else 1")
	flag.Parse()
	if *healthcheck {
		os.Exit(runHealthcheck(os.Getenv("SCRAPER_LISTEN_ADDR")))
	}
	cfg, err := ss.LoadConfig(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scraper:", err)
		os.Exit(1)
	}
	log := ss.DefaultLogger()
	registerPlatforms(cfg)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           ss.NewServer(cfg, ss.Default, log),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("scraper listening", "addr", cfg.ListenAddr)
	select {
	case err := <-errc:
		log.Error("scraper stopped", "error", err.Error())
		os.Exit(1)
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("shutdown", "error", err.Error())
		os.Exit(1)
	}
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
