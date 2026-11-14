package socialscraper

import (
	"errors"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Oxylabs API selectors for OXYLABS_API.
const (
	APIWeb        = "web_api"
	APIWebScraper = "web_scraper_api"
	APIAIStudio   = "ai_studio"
)

// DefaultAIStudioURL is the OXYLABS_AI_STUDIO_URL default, applied by the web module while WebConfig.AIStudioURL is empty.
const DefaultAIStudioURL = "https://api-aistudio.oxylabs.io"

// WebConfig configures the web page module. Credentials are only checked when a page is scraped
// (platform_not_configured), so a deployment without Oxylabs still starts.
type WebConfig struct {
	API          string        // OXYLABS_API: web_api (default), web_scraper_api or ai_studio
	WebAPIKey    string        // OXYLABS_WEB_API_KEY (web_api)
	Username     string        // OXYLABS_USERNAME (web_scraper_api)
	Password     string        // OXYLABS_PASSWORD (web_scraper_api)
	AIStudioKey  string        // OXYLABS_AI_STUDIO_API_KEY (ai_studio)
	AIStudioURL  string        // OXYLABS_AI_STUDIO_URL, "" means DefaultAIStudioURL
	Location     string        // OXYLABS_LOCATION: two-letter country code, upper-case, or ""
	MinTextChars int           // WEB_MIN_TEXT_CHARS, default 300
	CacheTTL     time.Duration // WEB_CACHE_TTL, default 1h
	Timeout      time.Duration // WEB_TIMEOUT_SECONDS: bound of a rendered fetch, default max(upstream timeout, 150 s)
}

// Config is read from the environment only. Error messages name a variable, never its value.
type Config struct {
	Timeout time.Duration // SCRAPER_UPSTREAM_TIMEOUT_SECONDS: bound of every outbound platform request
	Web     WebConfig
}

var countryRe = regexp.MustCompile(`^[A-Za-z]{2}$`)

// seconds parses a positive number of seconds (decimals allowed) for the variable name.
func seconds(getenv func(string) string, name string) (time.Duration, bool, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return 0, false, nil
	}
	s, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(s) || s <= 0 || s >= 9e9 { // 9e9 s: time.Duration would overflow
		return 0, false, errors.New(name + " must be a positive number")
	}
	d := time.Duration(s * float64(time.Second))
	if d <= 0 {
		return 0, false, errors.New(name + " must be a positive number")
	}
	return d, true, nil
}

// LoadConfig reads the configuration through getenv.
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{Timeout: 30 * time.Second}
	if d, ok, err := seconds(getenv, "SCRAPER_UPSTREAM_TIMEOUT_SECONDS"); err != nil {
		return c, err
	} else if ok {
		c.Timeout = d
	}
	w := &c.Web
	*w = WebConfig{API: APIWeb, MinTextChars: 300, CacheTTL: time.Hour, Timeout: max(c.Timeout, 150*time.Second),
		WebAPIKey: strings.TrimSpace(getenv("OXYLABS_WEB_API_KEY")),
		Username:  strings.TrimSpace(getenv("OXYLABS_USERNAME")), Password: strings.TrimSpace(getenv("OXYLABS_PASSWORD")),
		AIStudioKey: strings.TrimSpace(getenv("OXYLABS_AI_STUDIO_API_KEY"))}
	switch v := strings.TrimSpace(getenv("OXYLABS_API")); v {
	case "":
	case APIWeb, APIWebScraper, APIAIStudio:
		w.API = v
	default:
		return c, errors.New("OXYLABS_API must be web_api, web_scraper_api or ai_studio")
	}
	if v := strings.TrimSpace(getenv("OXYLABS_AI_STUDIO_URL")); v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return c, errors.New("OXYLABS_AI_STUDIO_URL must be an absolute http(s) URL")
		}
		w.AIStudioURL = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(getenv("OXYLABS_LOCATION")); v != "" {
		if !countryRe.MatchString(v) {
			return c, errors.New("OXYLABS_LOCATION must be a two-letter country code")
		}
		w.Location = strings.ToUpper(v)
	}
	if v := strings.TrimSpace(getenv("WEB_MIN_TEXT_CHARS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return c, errors.New("WEB_MIN_TEXT_CHARS must be a positive integer")
		}
		w.MinTextChars = n
	}
	if v := strings.TrimSpace(getenv("WEB_CACHE_TTL")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return c, errors.New("WEB_CACHE_TTL must be a positive duration")
		}
		w.CacheTTL = d
	}
	if d, ok, err := seconds(getenv, "WEB_TIMEOUT_SECONDS"); err != nil {
		return c, err
	} else if ok {
		w.Timeout = d
	}
	return c, nil
}
