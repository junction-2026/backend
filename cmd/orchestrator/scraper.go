package main

import (
	"log/slog"
	"net/http"

	"github.com/junction-2026/backend/internal/scraper"
	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/bluesky"
	"github.com/junction-2026/backend/internal/socialscraper/instagram"
	"github.com/junction-2026/backend/internal/socialscraper/mastodon"
	"github.com/junction-2026/backend/internal/socialscraper/threads"
	"github.com/junction-2026/backend/internal/socialscraper/x"
)

// upstreamTransport carries every platform request; nil means a clone of http.DefaultTransport.
// Tests route it to scrapertest fixtures so no platform is reached.
var upstreamTransport http.RoundTripper

// newScraper builds the platform registry over one shared client, bounded per request by the
// upstream timeout, and returns the in-process scraper the engine calls.
func newScraper(getenv func(string) string, cfg ss.Config, log *slog.Logger, redactor *ss.Redactor) *scraper.Client {
	hc := ss.NewClient(cfg.Timeout, upstreamTransport)
	reg := ss.NewRegistry()
	reg.Register(ss.Bluesky, bluesky.New(hc))
	reg.Register(ss.Mastodon, mastodon.New(hc))
	reg.Register(ss.Instagram, instagram.New(hc, getenv("INSTAGRAM_SESSION_FILE"), log))
	reg.Register(ss.Threads, threads.New(hc))
	reg.Register(ss.Twitter, x.New(hc, getenv))
	return &scraper.Client{Core: ss.New(reg, log), Redactor: redactor}
}
