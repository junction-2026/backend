package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/chat"
	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/localization"
	"github.com/junction-2026/backend/internal/risk"
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
	// Chat is the CHAT_* configuration of POST /api/v1/jobs/{id}/chat; an empty Chat.APIKey disables chat.
	Chat chat.Config
	// Country is the COUNTRY_ASSOCIATION_* configuration; an empty Country.Model leaves the step not configured.
	Country adapter.CountryConfig
}

// obsoleteScraperVars belonged to the former standalone scraper service.
var obsoleteScraperVars = []string{"SCRAPER_URL", "SCRAPER_TOKEN", "SCRAPER_LISTEN_ADDR"}

// loadConfig reads LISTEN_ADDR, ANALYZERS_CONFIG, the engine settings
// (MAX_PARALLEL_ANALYZERS, MAX_PARALLEL_JOBS, JOB_RETENTION, SCRAPER_TIMEOUT) and
// RISK_SCORE_CONFIG (optional YAML file), SCRAPER_ENABLED, SCRAPER_UPSTREAM_TIMEOUT_SECONDS, LOCALIZATION_TIMEOUT, the CHAT_* and the COUNTRY_ASSOCIATION_* variables. Errors name variables, never their values.
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
	if c.Engine.Risk, err = risk.Load(getenv("RISK_SCORE_CONFIG")); err != nil {
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
	if err := loadChat(getenv, &c.Chat); err != nil {
		return c, fmt.Errorf("configuration error: %w", err)
	}
	if err := loadCountry(getenv, &c.Country); err != nil {
		return c, fmt.Errorf("configuration error: %w", err)
	}
	return c, nil
}

// loadCountry reads COUNTRY_ASSOCIATION_MODEL, _BASE_URL (required with a model), _API_KEY, _TIMEOUT (default 30s)
// and _MAX_INPUT_CHARS (default 8000). Without a model the other variables are not checked.
func loadCountry(getenv func(string) string, c *adapter.CountryConfig) error {
	if c.Model = strings.TrimSpace(getenv("COUNTRY_ASSOCIATION_MODEL")); c.Model == "" {
		return nil
	}
	c.BaseURL = getenv("COUNTRY_ASSOCIATION_BASE_URL")
	if u, err := url.Parse(c.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("COUNTRY_ASSOCIATION_BASE_URL must be an http or https URL when COUNTRY_ASSOCIATION_MODEL is set")
	}
	c.APIKey = strings.TrimSpace(getenv("COUNTRY_ASSOCIATION_API_KEY"))
	c.Timeout = adapter.DefaultCountryTimeout
	if v := getenv("COUNTRY_ASSOCIATION_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return errors.New("COUNTRY_ASSOCIATION_TIMEOUT must be a positive duration")
		}
		c.Timeout = d
	}
	var err error
	c.MaxInputChars, err = positiveInt(getenv, "COUNTRY_ASSOCIATION_MAX_INPUT_CHARS", adapter.DefaultCountryMaxInput)
	return err
}

// loadChat reads the CHAT_* variables. A missing key is not an error (chat answers 503); the file wins over
// CHAT_API_KEY. Errors name the variable and never its value.
func loadChat(getenv func(string) string, c *chat.Config) error {
	c.BaseURL, c.Model = chat.DefaultBaseURL, chat.DefaultModel
	if v := getenv("CHAT_BASE_URL"); v != "" {
		if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("CHAT_BASE_URL must be an http or https URL")
		}
		c.BaseURL = v
	}
	if v := getenv("CHAT_MODEL"); v != "" {
		c.Model = v
	}
	c.APIKey = strings.TrimSpace(getenv("CHAT_API_KEY"))
	if path := getenv("CHAT_API_KEY_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return errors.New("CHAT_API_KEY_FILE cannot be read")
		}
		c.APIKey = strings.TrimSpace(string(b))
	}
	c.Timeout = chat.DefaultTimeout
	if v := getenv("CHAT_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return errors.New("CHAT_TIMEOUT must be a positive duration")
		}
		c.Timeout = d
	}
	var err error
	if c.RatePerMinute, err = positiveInt(getenv, "CHAT_RATE_PER_MINUTE", chat.DefaultRatePerMinute); err != nil {
		return err
	}
	c.MaxConcurrent, err = positiveInt(getenv, "CHAT_MAX_CONCURRENT", chat.DefaultMaxConcurrent)
	return err
}

func positiveInt(getenv func(string) string, name string, def int) (int, error) {
	v := getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, errors.New(name + " must be a positive integer")
	}
	return n, nil
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
