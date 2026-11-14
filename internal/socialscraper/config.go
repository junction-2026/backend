package socialscraper

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

// Config is read from the environment only. Error messages name a variable, never its value.
type Config struct {
	Timeout time.Duration // SCRAPER_UPSTREAM_TIMEOUT_SECONDS: bound of every outbound platform request
}

// LoadConfig reads the configuration through getenv.
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{Timeout: 30 * time.Second}
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
	return c, nil
}
