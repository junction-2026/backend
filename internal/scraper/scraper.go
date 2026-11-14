// Package scraper is the orchestrator's client of the social-scraper-api
// (POST /scrape) and maps a ScrapeResult to the job Input.
package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/junction-2026/backend/internal/job"
)

const maxResponse = 16 << 20 // max_posts ≤ 100 and max_comments ≤ 200 keep real bodies far below

// Client calls POST {URL}/scrape with Authorization: Bearer {Token}.
type Client struct {
	URL   string
	Token string
	HTTP  *http.Client // default http.DefaultClient; the context bounds every call
}

// Result is a successful scrape mapped to the job.
type Result struct {
	Input    job.Input // content, content_type text/plain and source; NormalizedContent is left to the engine
	Platform string
	URLKind  string
	Warnings []json.RawMessage
}

type post struct {
	Text      string  `json:"text"`
	CreatedAt *string `json:"created_at"`
}

// scrapeResult holds the members the mapping needs; everything else stays in the raw body.
type scrapeResult struct {
	Platform    string            `json:"platform"`
	URLKind     string            `json:"url_kind"`
	Username    string            `json:"username"`
	DisplayName *string           `json:"display_name"`
	Post        *post             `json:"post"`
	Posts       []post            `json:"posts"`
	Warnings    []json.RawMessage `json:"warnings"`
}

func fail(code, msg string) *job.ScrapeError { return &job.ScrapeError{Code: code, Message: msg} }

var errUnavailable = fail(job.CodeScraperUnavailable, "the scraper service is unavailable")

// Scrape runs one scrape. The error is always a *job.ScrapeError.
func (c *Client) Scrape(ctx context.Context, req job.ScrapeRequest) (*Result, error) {
	res, err := c.scrape(ctx, req)
	if err != nil && ctx.Err() != nil { // the call failed because ctx ended
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fail(job.CodeTimeout, "the scraper did not answer within the scrape timeout")
		}
		return nil, fail(job.CodeCancelled, "scrape aborted: job cancelled")
	}
	return res, err
}

func (c *Client) scrape(ctx context.Context, req job.ScrapeRequest) (*Result, error) {
	body, _ := json.Marshal(req)
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/scrape", bytes.NewReader(body))
	if err != nil {
		return nil, errUnavailable
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(hr)
	if err != nil {
		return nil, errUnavailable // err names the URL: never surfaced
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return nil, errUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.mapError(resp.StatusCode, raw)
	}
	return mapResult(req.URL, raw)
}

// mapError maps a scraper error response (social-scraper-api error codes).
func (c *Client) mapError(status int, raw []byte) *job.ScrapeError {
	var env struct {
		Error *job.ScrapeError `json:"error"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Error == nil || env.Error.Code == "" {
		return errUnavailable
	}
	switch e := env.Error; {
	case status == http.StatusUnauthorized, status == http.StatusInternalServerError,
		e.Code == "unauthorized", e.Code == "invalid_request", e.Code == "validation_failed", e.Code == "internal_error":
		return errUnavailable // our request or our credentials are wrong, not the user's URL
	default:
		msg := e.Message
		for _, secret := range []string{c.Token, strings.TrimRight(c.URL, "/")} {
			if secret != "" {
				msg = strings.ReplaceAll(msg, secret, "[redacted]")
			}
		}
		if msg == "" {
			msg = "scrape failed: " + e.Code
		}
		if len(e.Details) > 0 && e.Details[0] != '{' { // details must be an object; drop anything else
			e.Details = nil
		}
		return &job.ScrapeError{Code: e.Code, Message: msg, Details: e.Details}
	}
}

// mapResult validates a 200 body and maps it to the Input (shared "Scraped input content/source").
func mapResult(url string, raw []byte) (*Result, error) {
	var r scrapeResult
	if json.Unmarshal(raw, &r) != nil || // also rejects non-objects; null leaves Platform empty
		r.Platform == "" || r.URLKind == "" || (r.URLKind == "post" && r.Post == nil) {
		return nil, errUnavailable
	}
	in := job.Input{ContentType: "text/plain", Source: job.Source{URL: url, Platform: r.Platform, Author: r.Username}}
	if r.DisplayName != nil {
		in.Source.Title = *r.DisplayName
	}
	if r.URLKind == "post" {
		in.Content = r.Post.Text
		if r.Post.CreatedAt != nil {
			if t, err := time.Parse(time.RFC3339, *r.Post.CreatedAt); err == nil {
				t = t.UTC()
				in.Source.PublishedAt = &t
			}
		}
	} else { // profile (and, tolerantly, any unknown kind): the history, newest first as delivered
		var texts []string
		for _, p := range r.Posts {
			if strings.TrimSpace(p.Text) != "" {
				texts = append(texts, p.Text)
			}
		}
		in.Content = strings.Join(texts, "\n\n")
	}
	if strings.TrimSpace(in.Content) == "" {
		return nil, fail(job.CodeNoTextContent, "the scraped "+r.URLKind+" has no text to analyze")
	}
	in.Source.Metadata, _ = json.Marshal(map[string]json.RawMessage{"scrape": bytes.TrimSpace(raw)})
	if r.Warnings == nil {
		r.Warnings = []json.RawMessage{}
	}
	return &Result{Input: in, Platform: r.Platform, URLKind: r.URLKind, Warnings: r.Warnings}, nil
}
