package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/localization"
	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const (
	defaultListenAddr      = ":8080"
	defaultAnalyzersConfig = "/etc/orchestrator/analyzers"
	defaultJobStorePath    = "data/jobs.db"
)

type config struct {
	ListenAddr      string
	AnalyzersConfig string
	Engine          engine.Config
	JobStore        string    // JOB_STORE: "memory" (default) or "sqlite"
	JobStorePath    string    // JOB_STORE_PATH (sqlite only)
	ScraperEnabled  bool      // SCRAPER_ENABLED, default true
	Upstream        ss.Config // SCRAPER_UPSTREAM_TIMEOUT_SECONDS
	Ignored         []string  // obsolete scraper variables that are set: warned about, never read
	// LocalizationTimeout (LOCALIZATION_TIMEOUT, default 20s) bounds a localization provider call.
	LocalizationTimeout time.Duration
}

// obsoleteScraperVars belonged to the former standalone scraper service.
var obsoleteScraperVars = []string{"SCRAPER_URL", "SCRAPER_TOKEN", "SCRAPER_LISTEN_ADDR"}

// loadConfig reads LISTEN_ADDR, ANALYZERS_CONFIG, the engine settings
// (MAX_PARALLEL_ANALYZERS, MAX_PARALLEL_JOBS, JOB_RETENTION, SCRAPER_TIMEOUT) and
// SCRAPER_ENABLED, SCRAPER_UPSTREAM_TIMEOUT_SECONDS, LOCALIZATION_TIMEOUT. Errors name variables, never their values.
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
	if err := loadStore(getenv, &c); err != nil {
		return c, fmt.Errorf("configuration error: %w", err)
	}
	if err := loadScraper(getenv, &c); err != nil {
		return c, fmt.Errorf("configuration error: %w", err)
	}
	c.LocalizationTimeout = localization.DefaultTimeout
	if v := getenv("LOCALIZATION_TIMEOUT"); v != "" {
		if c.LocalizationTimeout, err = time.ParseDuration(v); err != nil || c.LocalizationTimeout <= 0 {
			return c, errors.New("configuration error: LOCALIZATION_TIMEOUT must be a positive duration")
		}
	}
	return c, nil
}

// loadStore reads JOB_STORE and JOB_STORE_PATH. JOB_HISTORY_RETENTION (read with the engine settings) only applies
// to the sqlite store, which is the only one with history.
func loadStore(getenv func(string) string, c *config) error {
	switch v := strings.ToLower(getenv("JOB_STORE")); v {
	case "", "memory":
		c.JobStore = "memory"
		c.Engine.HistoryRetention = 0
	case "sqlite":
		c.JobStore, c.JobStorePath = "sqlite", getenv("JOB_STORE_PATH")
		if c.JobStorePath == "" {
			c.JobStorePath = defaultJobStorePath
		}
	default:
		return errors.New("JOB_STORE must be memory or sqlite")
	}
	return nil
}

func loadScraper(getenv func(string) string, c *config) error {
	if v := getenv("SCRAPER_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return errors.New("SCRAPER_TIMEOUT must be a positive duration")
		}
		c.Engine.ScrapeTimeout = d
	}
	switch v := strings.ToLower(getenv("SCRAPER_ENABLED")); v {
	case "", "true":
		c.ScraperEnabled = true
	case "false":
	default:
		return errors.New("SCRAPER_ENABLED must be true or false")
	}
	var err error
	if c.Upstream, err = ss.LoadConfig(getenv); err != nil {
		return err
	}
	for _, name := range obsoleteScraperVars {
		if getenv(name) != "" {
			c.Ignored = append(c.Ignored, name)
		}
	}
	return nil
}
