// Package scraper adapts the in-process scraper core (internal/socialscraper) to the engine: it runs
// one scrape, maps a ScrapeResult to the job Input and a scrape error to the job's Scrape.error.
package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	neturl "net/url"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/junction-2026/backend/internal/job"
	ss "github.com/junction-2026/backend/internal/socialscraper"
)

// Client calls the scraper core directly: no HTTP hop, no token.
type Client struct {
	Core     *ss.Scraper
	Redactor *ss.Redactor // nil: error messages are passed through as the core wrote them
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
	Platform    string  `json:"platform"`
	URLKind     string  `json:"url_kind"`
	Username    string  `json:"username"`
	DisplayName *string `json:"display_name"`
	Post        *post   `json:"post"`
	Posts       []post  `json:"posts"`
	// url_kind page
	Text         *string           `json:"text"`
	FinalURL     string            `json:"final_url"`
	Domain       *string           `json:"domain"`
	CanonicalURL *string           `json:"canonical_url"`
	Title        *string           `json:"title"`
	Author       *string           `json:"author"`
	PublishedAt  *string           `json:"published_at"`
	Warnings     []json.RawMessage `json:"warnings"`
}

func fail(code, msg string) *job.ScrapeError { return &job.ScrapeError{Code: code, Message: msg} }

var errUnavailable = fail(job.CodeScraperUnavailable, "the scraper is unavailable")

// Scrape runs one scrape bounded by ctx. The error is always a *job.ScrapeError.
func (c *Client) Scrape(ctx context.Context, req job.ScrapeRequest) (*Result, error) {
	res, err := c.scrape(ctx, req)
	if err != nil && ctx.Err() != nil { // the scrape failed because ctx ended
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fail(job.CodeTimeout, "the scrape did not finish within the scrape timeout")
		}
		return nil, fail(job.CodeCancelled, "scrape aborted: job cancelled")
	}
	return res, err
}

func (c *Client) scrape(ctx context.Context, req job.ScrapeRequest) (*Result, error) {
	res, serr := c.Core.Scrape(ctx, req.URL, ss.Request{MaxPosts: req.MaxPosts, MaxComments: req.MaxComments})
	if serr != nil {
		return nil, c.mapError(serr)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, errUnavailable
	}
	return mapResult(req.URL, raw)
}

// mapError maps a scrape error (social-scraper-api error codes); internal_error means our failure, not the user's URL.
func (c *Client) mapError(e *ss.Error) *job.ScrapeError {
	if e.Code == "" || e.Code == "internal_error" {
		return errUnavailable
	}
	msg := e.Message
	if c.Redactor != nil {
		msg = c.Redactor.Redact(msg)
	}
	if msg == "" {
		msg = "scrape failed: " + e.Code
	}
	out := &job.ScrapeError{Code: e.Code, Message: msg}
	if len(e.Details) > 0 {
		out.Details, _ = json.Marshal(e.Details) // a value that cannot be encoded drops the details, not the error
	}
	return out
}

// pageURL is the canonical URL when it is on the fetched page's registered domain (a page cannot borrow another
// outlet's record), otherwise the final URL, otherwise the requested one.
func pageURL(requested string, r *scrapeResult) string {
	if r.CanonicalURL != nil && r.Domain != nil {
		if u, err := neturl.Parse(*r.CanonicalURL); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			if d, err := publicsuffix.EffectiveTLDPlusOne(strings.ToLower(u.Hostname())); err == nil && d == *r.Domain {
				return *r.CanonicalURL
			}
		}
	}
	if r.FinalURL != "" {
		return r.FinalURL
	}
	return requested
}

// mapResult validates a ScrapeResult and maps it to the Input (shared "Scraped input content/source").
func mapResult(url string, raw []byte) (*Result, error) {
	var r scrapeResult
	if json.Unmarshal(raw, &r) != nil || // also rejects non-objects; null leaves Platform empty
		r.Platform == "" || r.URLKind == "" || (r.URLKind == "post" && r.Post == nil) || (r.URLKind == "page" && r.Text == nil) {
		return nil, errUnavailable
	}
	in := job.Input{ContentType: "text/plain", Source: job.Source{URL: url, Platform: r.Platform, Author: r.Username}}
	if r.DisplayName != nil {
		in.Source.Title = *r.DisplayName
	}
	switch r.URLKind {
	case "page": // shared "Scraped page source"
		in.Content = *r.Text
		in.Source.URL = pageURL(url, &r)
		in.Source.Author, in.Source.Title = "", ""
		if r.Title != nil {
			in.Source.Title = *r.Title
		}
		if r.Author != nil {
			in.Source.Author = *r.Author
		}
		if r.PublishedAt != nil {
			if t, err := time.Parse(time.RFC3339, *r.PublishedAt); err == nil {
				t = t.UTC()
				in.Source.PublishedAt = &t
			}
		}
	case "post":
		in.Content = r.Post.Text
		if r.Post.CreatedAt != nil {
			if t, err := time.Parse(time.RFC3339, *r.Post.CreatedAt); err == nil {
				t = t.UTC()
				in.Source.PublishedAt = &t
			}
		}
	default: // profile (and, tolerantly, any unknown kind): the history, newest first as delivered
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
