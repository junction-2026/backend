// Package socialscraper implements the shared social-scraper-api: the HTTP server, request validation,
// URL recognition, the error envelope and the platform-module interface. Platform modules live in
// subpackages and are registered from cmd/scraper.
package socialscraper

import (
	"context"
	"net/url"
)

// Request is the validated scrape request (defaults applied).
type Request struct {
	MaxPosts    int
	MaxComments int
}

// Target is a recognized URL.
type Target struct {
	Platform string            // bluesky, mastodon, instagram, threads, facebook, twitter
	URLKind  string            // profile or post
	URL      *url.URL          // the parsed request URL
	IDs      map[string]string // platform-specific identifiers taken from the URL
}

// Module scrapes one platform. It returns *Error for typed failures; any other error becomes 500
// internal_error, except context deadline errors, which become 504 upstream_timeout. The server sets
// ScrapedAt and LatencyMS and replaces nil slices with empty ones.
type Module interface {
	Scrape(ctx context.Context, t Target, req Request) (*Result, error)
}

type Author struct {
	Username    string  `json:"username"`
	DisplayName *string `json:"display_name"`
}

type Post struct {
	ID        string  `json:"id"`
	Text      string  `json:"text"`
	CreatedAt *string `json:"created_at"`
	URL       *string `json:"url"`
	Likes     *int    `json:"likes"`
	Replies   *int    `json:"replies"`
	Reposts   *int    `json:"reposts"`
}

type Comment struct {
	Post
	Author      Author  `json:"author"`
	InReplyToID *string `json:"in_reply_to_id"`
}

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Result is the ScrapeResult of the shared spec.
type Result struct {
	Platform    string    `json:"platform"`
	URLKind     string    `json:"url_kind"`
	Username    string    `json:"username"`
	DisplayName *string   `json:"display_name"`
	Bio         *string   `json:"bio"`
	ProfileURL  *string   `json:"profile_url"`
	Followers   *int      `json:"followers"`
	Following   *int      `json:"following"`
	PostCount   *int      `json:"post_count"`
	Verified    *bool     `json:"verified"`
	ScrapedAt   string    `json:"scraped_at"`
	LatencyMS   int64     `json:"latency_ms"`
	Posts       []Post    `json:"posts"`
	Post        *Post     `json:"post"`
	Comments    []Comment `json:"comments"`
	Warnings    []Warning `json:"warnings"`
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }

// NonEmpty returns nil for "" (a member the platform left blank is null, not "").
func NonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
