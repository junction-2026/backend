// Package chat answers questions about a completed analysis through a self-hosted OpenAI-compatible agent that the
// product presents as VEIL (shared capability analysis-chat-api, local capability analysis-chat).
package chat

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/localization"
)

// Defaults of the CHAT_* settings.
const (
	DefaultBaseURL       = "http://127.0.0.1:8642/v1"
	DefaultModel         = "hermes-agent"
	DefaultTimeout       = 90 * time.Second
	DefaultRatePerMinute = 10
	DefaultMaxConcurrent = 4

	MaxReply    = 8000  // code points of a reply
	MaxWindow   = 20    // client messages forwarded
	MaxGround   = 48000 // code points of the grounding message
	maxUpstream = 4 << 20
)

// Errors of Reply; their text never carries upstream data.
var (
	ErrUnavailable = errors.New("chat unavailable")
	ErrTimeout     = errors.New("chat timeout")
)

//go:embed persona.txt
var persona string

// Config is the chat configuration; an empty APIKey disables chat.
type Config struct {
	BaseURL       string
	Model         string
	APIKey        string // secret: never logged or returned
	Timeout       time.Duration
	RatePerMinute int
	MaxConcurrent int
}

// Message is a conversation turn; Role is "user" or "assistant".
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Service calls the upstream agent. A nil *Service is a disabled one.
type Service struct {
	cfg    Config
	client *http.Client
	log    *slog.Logger
	lim    *limiter
}

// New returns the service; zero Config fields take their defaults (except APIKey).
func New(cfg Config, log *slog.Logger) *Service {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.RatePerMinute <= 0 {
		cfg.RatePerMinute = DefaultRatePerMinute
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if log == nil {
		log = slog.Default()
	}
	return &Service{cfg: cfg, client: &http.Client{}, log: log, lim: newLimiter(cfg.RatePerMinute, cfg.MaxConcurrent, time.Now)}
}

// Enabled reports whether chat is configured (an API key is set).
func (s *Service) Enabled() bool { return s != nil && s.cfg.APIKey != "" }

// Window returns the part of msgs the assistant gets and whether older messages were dropped: the latest MaxWindow,
// starting with a user message (one more is dropped if needed).
func Window(msgs []Message) ([]Message, bool) {
	if len(msgs) <= MaxWindow {
		return msgs, false
	}
	w := msgs[len(msgs)-MaxWindow:]
	if w[0].Role != "user" {
		w = w[1:]
	}
	return w, true
}

// Acquire admits a request for jobID or returns the Retry-After seconds (>= 1). Release must be called once after
// an admitted request.
func (s *Service) Acquire(jobID string) (release func(), retryAfter int) { return s.lim.acquire(jobID) }

// Reply asks the agent about job j. history is the already windowed conversation (non-empty, ends with a user
// message). It returns ErrUnavailable or ErrTimeout, logging only the job id, outcome and duration.
func (s *Service) Reply(ctx context.Context, j job.Job, history []Message, language string) (string, error) {
	start := time.Now()
	text, err := s.call(ctx, j, history, language)
	outcome := "ok"
	switch {
	case errors.Is(err, ErrTimeout):
		outcome = "chat_timeout"
	case err != nil:
		outcome = "chat_unavailable"
	}
	s.log.Info("chat", "job_id", j.ID, "outcome", outcome, "duration_ms", time.Since(start).Milliseconds())
	return text, err
}

func (s *Service) call(ctx context.Context, j job.Job, history []Message, language string) (string, error) {
	msgs := make([]Message, 0, len(history)+2)
	msgs = append(msgs, Message{Role: "system", Content: systemPrompt(language)}, Message{Role: "system", Content: Ground(j)})
	msgs = append(msgs, history...)
	body, err := json.Marshal(map[string]any{"model": s.cfg.Model, "messages": msgs, "stream": false})
	if err != nil {
		return "", ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", s.fail(ctx)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstream))
	if err != nil {
		return "", s.fail(ctx)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", ErrUnavailable
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &out) != nil || len(out.Choices) == 0 {
		return "", ErrUnavailable
	}
	text := s.scrub(strings.TrimSpace(out.Choices[0].Message.Content))
	if r := []rune(text); len(r) > MaxReply {
		text = strings.TrimSpace(string(r[:MaxReply]))
	}
	if text == "" {
		return "", ErrUnavailable
	}
	return text, nil
}

// fail classifies a transport error: only our own timeout is a timeout.
func (s *Service) fail(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrTimeout
	}
	return ErrUnavailable
}

// scrub is defence in depth: an upstream that echoes the key or the base URL must not pass them on.
func (s *Service) scrub(t string) string {
	for _, secret := range []string{s.cfg.APIKey, s.cfg.BaseURL} {
		if secret != "" {
			t = strings.ReplaceAll(t, secret, "[redacted]")
		}
	}
	return t
}

var languageNames = map[string]string{"en": "English", "fr": "French", "ar": "Arabic", "de": "German", "fi": "Finnish", "ru": "Russian"}

// systemPrompt is the persona plus the answer-language instruction. language is the client's hint.
func systemPrompt(language string) string {
	lang, ok := localization.Language(language)
	if ok {
		return persona + "\nLanguage: answer in " + languageNames[lang] + ". The name VEIL stays VEIL."
	}
	return persona + "\nLanguage: answer in the language of the user's last message. The name VEIL stays VEIL."
}

// runeLen is the length in Unicode code points.
func runeLen(s string) int { return utf8.RuneCountInString(s) }
