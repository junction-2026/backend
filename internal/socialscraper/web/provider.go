package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const (
	defaultWebAPIURL        = "https://webapi.oxylabs.io/v1/scrape"
	defaultWebScraperAPIURL = "https://realtime.oxylabs.io/v1/queries"
)

// fetched is the provider answer mapped to one internal shape, whichever Oxylabs API produced it.
type fetched struct {
	HTML, Markdown string
	FinalURL       string
	TargetStatus   int // 0 when the provider did not report it
}

// providerError is a failed provider request: status is the provider's HTTP status, 0 for a transport failure.
type providerError struct {
	status     int
	retryAfter int // seconds, -1 unknown
	err        *ss.Error
}

func (e *providerError) Error() string { return e.err.Error() }

// retryable reports the answers the module retries once: 429 and 5xx.
func (e *providerError) retryable() bool { return e.status == 429 || e.status >= 500 }

// buildRequest returns the provider request for one fetch of target (no fragment). Credentials are only in
// the headers, never in the URL or body.
func (m *Module) buildRequest(ctx context.Context, target string, render bool) (*http.Request, error) {
	var (
		endpoint string
		body     map[string]any
	)
	if m.cfg.API == ss.APIWebScraper {
		endpoint = orDefault(m.WebScraperAPIURL, defaultWebScraperAPIURL)
		body = map[string]any{"source": "universal", "url": target, "markdown": true}
		if render {
			body["render"] = "html"
		}
		if m.cfg.Location != "" {
			body["geo_location"] = m.cfg.Location
		}
	} else {
		endpoint = orDefault(m.WebAPIURL, defaultWebAPIURL)
		body = map[string]any{"url": target, "output": []string{"html", "markdown"}, "run_js": render, "device": "desktop"}
		if m.cfg.Location != "" {
			body["location"] = m.cfg.Location
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if m.cfg.API == ss.APIWebScraper {
		req.SetBasicAuth(m.cfg.Username, m.cfg.Password)
	} else {
		req.Header.Set("Authorization", "Bearer "+m.cfg.WebAPIKey)
	}
	return req, nil
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

// providerResult is the member set of results[0] of both APIs; unknown members are ignored.
type providerResult struct {
	HTML       string `json:"html"`
	Markdown   string `json:"markdown"`
	Content    string `json:"content"` // Web Scraper API: HTML, or Markdown when markdown was requested
	StatusCode int    `json:"status_code"`
	URL        string `json:"url"`
}

// fetch sends one provider request bounded by timeout and maps the answer; failures are *providerError.
func (m *Module) fetch(ctx context.Context, target string, render bool, timeout time.Duration) (*fetched, *providerError) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fail := func(status int, e *ss.Error) *providerError {
		return &providerError{status: status, retryAfter: -1, err: e}
	}
	req, err := m.buildRequest(ctx, target, render)
	if err != nil {
		return nil, fail(0, ss.UpstreamError(platform, "could not build the page provider request"))
	}
	resp, err := m.HTTP.Do(req)
	if err != nil {
		if ss.IsTimeout(err) {
			return nil, fail(0, ss.UpstreamTimeout(platform))
		}
		return nil, fail(0, ss.UpstreamError(platform, "could not reach the page provider")) // err may carry the URL, never the key
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		if ss.IsTimeout(rerr) {
			return nil, fail(0, ss.UpstreamTimeout(platform))
		}
		return nil, fail(resp.StatusCode, ss.UpstreamError(platform, "could not read the page provider answer"))
	}
	switch code := resp.StatusCode; {
	case code == http.StatusTooManyRequests:
		pe := fail(code, ss.RateLimited(platform, retryAfter(resp.Header)))
		pe.retryAfter = retryAfter(resp.Header)
		return nil, pe
	case code >= 500:
		return nil, fail(code, ss.UpstreamError(platform, "the page provider failed (HTTP "+strconv.Itoa(code)+")"))
	case code != http.StatusOK:
		return nil, fail(code, ss.UpstreamError(platform, "the page provider rejected the request (HTTP "+strconv.Itoa(code)+")"))
	}
	var doc struct {
		Results []providerResult `json:"results"`
	}
	if json.Unmarshal(body, &doc) != nil || len(doc.Results) == 0 {
		return nil, fail(code200, ss.UpstreamError(platform, "the page provider answered with data that could not be parsed"))
	}
	r := doc.Results[0]
	f := &fetched{HTML: r.HTML, Markdown: r.Markdown, FinalURL: r.URL, TargetStatus: r.StatusCode}
	if r.Content != "" && f.HTML == "" && f.Markdown == "" {
		if looksLikeHTML(r.Content) {
			f.HTML = r.Content
		} else {
			f.Markdown = r.Content
		}
	}
	return f, nil
}

const code200 = http.StatusOK

func looksLikeHTML(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "<") && (strings.Contains(s, "<html") || strings.Contains(s, "<body") || strings.HasPrefix(s, "<!doctype"))
}

func retryAfter(h http.Header) int {
	if n, err := strconv.Atoi(h.Get("Retry-After")); err == nil && n >= 0 {
		return n
	}
	return -1
}

// targetError maps a target status >= 400 reported by the provider.
func targetError(status int) *ss.Error {
	msg := "the page answered HTTP " + strconv.Itoa(status)
	if status == 401 || status == 403 {
		msg = "the page refused the request (HTTP " + strconv.Itoa(status) + ")"
	}
	e := ss.UpstreamError(platform, msg)
	e.Details["target_status"] = status
	return e
}

// requestKey is the cache key: lower-case scheme and host, no fragment, no default port, query kept.
func requestKey(u *url.URL) string {
	c := *u
	c.Fragment, c.RawFragment = "", ""
	c.Scheme = strings.ToLower(c.Scheme)
	h := strings.TrimSuffix(strings.ToLower(c.Hostname()), ".")
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	if p := c.Port(); p != "" && !(p == "80" && c.Scheme == "http") && !(p == "443" && c.Scheme == "https") {
		h += ":" + p
	}
	c.Host = h
	return c.String()
}
