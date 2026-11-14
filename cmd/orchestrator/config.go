package main

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/scraper"
)

const (
	defaultListenAddr      = ":8080"
	defaultAnalyzersConfig = "/etc/orchestrator/analyzers"
)

type config struct {
	ListenAddr      string
	AnalyzersConfig string
	Engine          engine.Config
}

// loadConfig reads LISTEN_ADDR, ANALYZERS_CONFIG and the engine settings
// (MAX_PARALLEL_ANALYZERS, MAX_PARALLEL_JOBS, JOB_RETENTION, SCRAPER_URL,
// SCRAPER_TOKEN, SCRAPER_TIMEOUT). Errors name variables, never their values.
func loadConfig(getenv func(string) string) (config, error) {
	c := config{ListenAddr: getenv("LISTEN_ADDR"), AnalyzersConfig: getenv("ANALYZERS_CONFIG")}
	if c.ListenAddr == "" {
		c.ListenAddr = defaultListenAddr
	}
	if c.AnalyzersConfig == "" {
		c.AnalyzersConfig = defaultAnalyzersConfig
	}
	var err error
	if c.Engine, err = engine.ConfigFromEnv(getenv); err != nil {
		return c, fmt.Errorf("configuration error: %w", err)
	}
	if err := loadScraper(getenv, &c.Engine); err != nil {
		return c, fmt.Errorf("configuration error: %w", err)
	}
	return c, nil
}

func loadScraper(getenv func(string) string, e *engine.Config) error {
	if v := getenv("SCRAPER_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return errors.New("SCRAPER_TIMEOUT must be a positive duration")
		}
		e.ScrapeTimeout = d
	}
	base := getenv("SCRAPER_URL")
	if base == "" {
		return nil // no scraper: scrape jobs answer 503
	}
	if u, err := url.Parse(base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("SCRAPER_URL must be an absolute http(s) URL")
	}
	token := getenv("SCRAPER_TOKEN")
	if token == "" {
		return errors.New("SCRAPER_TOKEN is required when SCRAPER_URL is set")
	}
	e.Scraper = &scraper.Client{URL: base, Token: token}
	return nil
}
