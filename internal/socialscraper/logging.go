package socialscraper

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
)

const redacted = "[redacted]"

var bearerRe = regexp.MustCompile(`(?i)(bearer\s+)[^\s,;"'}]+`)

// secretEnv are the environment variables whose values never reach a log line.
var secretEnv = []string{"SCRAPER_TOKEN", "TWITTER_AUTH_TOKEN", "TWITTER_CT0", "INSTAGRAM_SESSION_FILE"}

// Redactor replaces `Bearer <...>` values and the secret values it was built with by [redacted].
type Redactor struct{ secrets []string }

// NewRedactor reads the secret environment variables through getenv, and the content of the
// INSTAGRAM_SESSION_FILE file (whole text and every string value of a JSON object) through readFile.
func NewRedactor(getenv func(string) string, readFile func(string) ([]byte, error), extra ...string) *Redactor {
	seen := map[string]bool{}
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" {
			seen[v] = true
		}
	}
	for _, name := range secretEnv {
		add(getenv(name))
	}
	if path := getenv("INSTAGRAM_SESSION_FILE"); path != "" && readFile != nil {
		if data, err := readFile(path); err == nil {
			add(string(data))
			var obj map[string]any
			if json.Unmarshal(data, &obj) == nil {
				for _, v := range obj {
					if s, ok := v.(string); ok {
						add(s)
					}
				}
			}
		}
	}
	for _, v := range extra {
		add(v)
	}
	r := &Redactor{}
	for v := range seen {
		r.secrets = append(r.secrets, v)
	}
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) }) // longest first
	return r
}

func (r *Redactor) Redact(s string) string {
	s = bearerRe.ReplaceAllString(s, "${1}"+redacted)
	for _, v := range r.secrets {
		s = strings.ReplaceAll(s, v, redacted)
	}
	return s
}

// NewLogger writes JSON lines to w (os.Stderr in production) through the redactor: message and every
// attribute value (errors and stack traces included) are redacted before encoding.
func NewLogger(w io.Writer, r *Redactor) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Value.Kind() {
			case slog.KindString:
				a.Value = slog.StringValue(r.Redact(a.Value.String()))
			case slog.KindAny:
				a.Value = slog.StringValue(r.Redact(fmt.Sprint(a.Value.Any())))
			}
			return a
		},
	}))
}

// DefaultLogger is the production logger: JSON to stderr, redacting the process environment's secrets.
func DefaultLogger() *slog.Logger {
	return NewLogger(os.Stderr, NewRedactor(os.Getenv, os.ReadFile))
}
