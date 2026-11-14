package main

import (
	"log/slog"
	"net/http"
	"os"

	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/instagram"
	"github.com/junction-2026/backend/internal/socialscraper/threads"
)

func init() {
	platformInits = append(platformInits, func(_ ss.Config, hc *http.Client) {
		registerMeta(ss.Default, hc, os.Getenv("INSTAGRAM_SESSION_FILE"), ss.DefaultLogger())
	})
}

// registerMeta installs the Instagram and Threads modules; sessionFile is INSTAGRAM_SESSION_FILE.
func registerMeta(reg *ss.Registry, hc *http.Client, sessionFile string, log *slog.Logger) {
	reg.Register(ss.Instagram, instagram.New(hc, sessionFile, log))
	reg.Register(ss.Threads, threads.New(hc))
}
