// Package web scrapes any http(s) page that no platform module claims: the page is fetched through the
// Oxylabs Web API (default) or Web Scraper API, its main text is extracted with go-trafilatura and its
// metadata read from JSON-LD, Open Graph and HTML meta tags. The server never contacts the page itself.
package web

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const (
	platform    = ss.Web
	maxRequests = 3 // unrendered, rendered, and one 429/5xx retry
)

// Module is the web page platform module.
type Module struct {
	HTTP *http.Client
	// WebAPIURL and WebScraperAPIURL override the provider endpoints (tests); empty means Oxylabs.
	WebAPIURL, WebScraperAPIURL string

	cfg        ss.WebConfig
	upstream   time.Duration // bound of an unrendered fetch
	cache      *cache
	retryDelay time.Duration
	now        func() time.Time
}

// New returns the module over a copy of hc that never follows redirects (the Authorization header must not
// leave the provider). upstream bounds an unrendered fetch; cfg.Timeout bounds a rendered one. Neither the
// http.Client nor the module logs request headers or bodies.
func New(cfg ss.WebConfig, upstream time.Duration, hc *http.Client) *Module {
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	m := &Module{HTTP: &c, cfg: cfg, upstream: upstream, retryDelay: time.Second, now: time.Now}
	m.cache = newCache(cfg.CacheTTL, func() time.Time { return m.now() })
	return m
}

// missing names the unset provider settings.
func (m *Module) missing() []string {
	var out []string
	if m.cfg.API == ss.APIWebScraper {
		if m.cfg.Username == "" {
			out = append(out, "OXYLABS_USERNAME")
		}
		if m.cfg.Password == "" {
			out = append(out, "OXYLABS_PASSWORD")
		}
	} else if m.cfg.WebAPIKey == "" {
		out = append(out, "OXYLABS_WEB_API_KEY")
	}
	return out
}

// session counts the provider requests of one scrape and enforces the retry budget.
type session struct {
	m       *Module
	target  string
	n       int
	retried bool
}

// fetch sends one logical fetch: a 429/5xx answer is retried once per scrape.
func (s *session) fetch(ctx context.Context, render bool) (*fetched, *ss.Error) {
	timeout := s.m.upstream
	if render {
		timeout = s.m.cfg.Timeout
	}
	for {
		s.n++
		f, perr := s.m.fetch(ctx, s.target, render, timeout)
		if perr == nil {
			if f.TargetStatus >= 400 {
				return nil, targetError(f.TargetStatus)
			}
			return f, nil
		}
		if !perr.retryable() || s.retried || s.n >= maxRequests {
			return nil, perr.err
		}
		s.retried = true
		select {
		case <-time.After(s.m.retryDelay):
		case <-ctx.Done():
			return nil, ss.UpstreamTimeout(platform)
		}
	}
}

// Scrape implements socialscraper.Module.
func (m *Module) Scrape(ctx context.Context, t ss.Target, _ ss.Request) (*ss.Result, error) {
	if missing := m.missing(); len(missing) > 0 {
		return nil, ss.PlatformNotConfigured(platform, missing...)
	}
	key := requestKey(t.URL)
	if res, ok := m.cache.get(key); ok {
		return res, nil
	}
	u := *t.URL
	u.Fragment, u.RawFragment = "", ""
	s := &session{m: m, target: u.String()}

	min := m.cfg.MinTextChars
	f, err := s.fetch(ctx, false)
	if err != nil {
		return nil, err
	}
	rendered := false
	text := mainText(f.HTML, &u)
	if chars(text) < min {
		if f, err = s.fetch(ctx, true); err != nil {
			return nil, err
		}
		rendered = true
		text = mainText(f.HTML, &u)
	}

	final := &u
	if fu, perr := url.Parse(f.FinalURL); perr == nil && (fu.Scheme == "http" || fu.Scheme == "https") && fu.Host != "" {
		final = fu
	}
	doc := parseDoc(f.HTML)
	extraction := "trafilatura"
	if chars(text) < min { // the Markdown version may hold more text than the extracted main content
		if md := stripMarkdown(f.Markdown); chars(md) > chars(text) {
			text, extraction = md, "markdown_fallback"
		}
	}
	if chars(text) < min && doc.loginWall(final.Path) {
		return nil, ss.LoginRequired(platform)
	}
	if strings.TrimSpace(text) == "" {
		return nil, ss.NoTextContent(platform)
	}
	text, truncated := capText(text)
	meta := doc.metadata()
	host := strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(final.Hostname(), ".")), "www.")
	var domain *string
	if net.ParseIP(host) == nil {
		domain = registeredDomain(host)
	}
	res := &ss.Result{
		Platform: platform, URLKind: ss.KindPage,
		Page: &ss.Page{
			Text: text, RequestedURL: u.String(), FinalURL: final.String(), Host: host, Domain: domain,
			SiteName: meta.siteName, Title: meta.title, Description: meta.description, Lang: meta.lang,
			CanonicalURL: meta.canonical, PublishedAt: meta.publishedAt, Author: meta.author,
			WordCount: len(strings.Fields(text)), Truncated: truncated, Extraction: extraction,
			Rendered: rendered, Provider: "oxylabs", OxylabsRequests: s.n,
		},
	}
	m.cache.put(key, res)
	return res, nil
}

func chars(s string) int { return utf8.RuneCountInString(s) }
