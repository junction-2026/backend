package socialscraper

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

const DefaultListenAddr = "0.0.0.0:8003"

// Config is read from the environment only. Error messages name a variable, never its value.
type Config struct {
	Token      string
	Timeout    time.Duration // SCRAPER_UPSTREAM_TIMEOUT_SECONDS: bound of one scrape and of every outbound request
	ListenAddr string
}

// LoadConfig reads the configuration through getenv (os.Getenv in production).
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{Token: getenv("SCRAPER_TOKEN"), Timeout: 30 * time.Second, ListenAddr: DefaultListenAddr}
	if c.Token == "" {
		return c, errors.New("SCRAPER_TOKEN is required but not set: refusing to start without an auth token")
	}
	if raw := strings.TrimSpace(getenv("SCRAPER_UPSTREAM_TIMEOUT_SECONDS")); raw != "" {
		s, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(s) || s <= 0 || s >= 9e9 { // 9e9 s: time.Duration would overflow
			return c, errors.New("SCRAPER_UPSTREAM_TIMEOUT_SECONDS must be a positive number")
		}
		c.Timeout = time.Duration(s * float64(time.Second))
		if c.Timeout <= 0 {
			return c, errors.New("SCRAPER_UPSTREAM_TIMEOUT_SECONDS must be a positive number")
		}
	}
	if a := getenv("SCRAPER_LISTEN_ADDR"); a != "" {
		c.ListenAddr = a
	}
	return c, nil
}
