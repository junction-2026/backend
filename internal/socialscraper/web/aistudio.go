package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const maxMetaChars = 500

// userAgent identifies the backend to AI Studio; the version is the module version of the build ("dev" for local builds).
var userAgent = "junction-backend/" + func() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}()

// aiSchema is the openapi_schema of the JSON run. Every member is optional text; page_kind classifies the page.
var aiSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title":         map[string]any{"type": "string"},
		"site_name":     map[string]any{"type": "string"},
		"author":        map[string]any{"type": "string"},
		"published_at":  map[string]any{"type": "string", "description": "ISO 8601 publication date-time, empty if not shown"},
		"lang":          map[string]any{"type": "string", "description": "ISO 639-1 code of the article's language"},
		"canonical_url": map[string]any{"type": "string"},
		"description":   map[string]any{"type": "string"},
		"body": map[string]any{"type": "string", "description": "The main text of the page (article, post or other content) exactly as shown, " +
			"without navigation, menus, ads, cookie banners, comments or related links; empty if the page shows none"},
		"page_kind": map[string]any{"type": "string", "enum": []string{"article", "post", "other_content", "login_wall", "error_page", "consent_or_paywall"},
			"description": "What the page is: article, post, other_content, login_wall, error_page or consent_or_paywall"},
	},
}

// runError ends a run. fatal (HTTP 401/403 from AI Studio) also forbids the Markdown run.
type runError struct {
	err   *ss.Error
	fatal bool
}

type aiStudio struct {
	m    *Module
	base string
	n    int // runs started
}

func (a *aiStudio) do(ctx context.Context, method, path string, q url.Values, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(raw)
	}
	u := a.base + path
	if q != nil {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("x-api-key", a.m.cfg.AIStudioKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := a.m.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, err
}

// codeRe is the shape of a provider error code.
var codeRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// providerCode returns the provider's error_code when it is a short token that does not contain the credential.
func (a *aiStudio) providerCode(raw []byte) string {
	var d struct {
		ErrorCode any `json:"error_code"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return ""
	}
	if c, _ := d.ErrorCode.(string); !codeRe.MatchString(c) || strings.Contains(c, a.m.cfg.AIStudioKey) {
		return ""
	}
	return d.ErrorCode.(string)
}

func (a *aiStudio) failed(status int, raw []byte, msg string) *runError {
	e := ss.UpstreamError(platform, msg)
	if c := a.providerCode(raw); c != "" {
		e.Details["provider_error_code"] = c
	}
	return &runError{err: e, fatal: status == http.StatusUnauthorized || status == http.StatusForbidden}
}

// transport maps a request error: the run deadline or job cancellation is a timeout, anything else an upstream error
// (err may carry the URL, never the key, and is not shown).
func transport(err error) *runError {
	if ss.IsTimeout(err) || errors.Is(err, context.Canceled) {
		return &runError{err: ss.UpstreamTimeout(platform)}
	}
	return &runError{err: ss.UpstreamError(platform, "could not reach the page provider")}
}

// run starts one AI Studio run for target, polls it until it ends and returns its data member.
func (a *aiStudio) run(ctx context.Context, target string, json_ bool) (any, *runError) {
	ctx, cancel := context.WithTimeout(ctx, a.m.cfg.Timeout)
	defer cancel()
	body := map[string]any{"url": target, "output_format": "markdown", "render_javascript": true}
	if json_ {
		body["output_format"], body["openapi_schema"] = "json", aiSchema
	}
	a.n++
	status, raw, err := a.do(ctx, http.MethodPost, "/scrape", nil, body)
	if err != nil {
		return nil, transport(err)
	}
	if status/100 != 2 {
		return nil, a.failed(status, raw, "the page provider rejected the request (HTTP "+strconv.Itoa(status)+")")
	}
	var started struct {
		RunID string `json:"run_id"`
	}
	if json.Unmarshal(raw, &started) != nil || started.RunID == "" {
		return nil, a.failed(status, nil, "the page provider answered with data that could not be parsed")
	}
	q := url.Values{"run_id": {started.RunID}}

	for wait := a.m.pollStart; ; wait = min(wait*3/2, a.m.pollMax) {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, transport(ctx.Err())
		}
		status, raw, err = a.do(ctx, http.MethodGet, "/scrape/run", q, nil)
		if err != nil {
			return nil, transport(err)
		}
		if status/100 != 2 {
			return nil, a.failed(status, raw, "the page provider failed while polling the run (HTTP "+strconv.Itoa(status)+")")
		}
		var st struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(raw, &st) != nil {
			return nil, a.failed(status, nil, "the page provider answered with data that could not be parsed")
		}
		switch strings.ToLower(st.Status) {
		case "processing", "pending", "queued", "running":
			continue
		case "completed":
		default:
			return nil, a.failed(status, raw, "the page provider run failed")
		}
		break
	}

	status, raw, err = a.do(ctx, http.MethodGet, "/scrape/run/data", q, nil)
	if err != nil {
		return nil, transport(err)
	}
	if status/100 != 2 {
		return nil, a.failed(status, raw, "the page provider failed to deliver the run data (HTTP "+strconv.Itoa(status)+")")
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return nil, a.failed(status, nil, "the page provider answered with data that could not be parsed")
	}
	if d, ok := doc["data"]; ok {
		return d, nil
	}
	return doc, nil
}

// aiPage is the sanitized output of the JSON run.
type aiPage struct {
	body, kind                                  string
	title, site, author, description, canonical *string
	lang, published                             *string
}

// sanitize removes control characters (except \n and \t) and zero-width characters from untrusted model output.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r), r == '\u200B', r == '\u200C', r == '\u200D', r == '\u2060', r == '\uFEFF':
			return -1
		}
		return r
	}, s)
}

// metaString is a metadata string: sanitized, trimmed, at most maxMetaChars characters, nil when empty.
func metaString(v any) *string {
	s, _ := v.(string)
	s = strings.TrimSpace(sanitize(s))
	if r := []rune(s); len(r) > maxMetaChars {
		s = strings.TrimSpace(string(r[:maxMetaChars]))
	}
	if s == "" {
		return nil
	}
	return &s
}

var langRe = regexp.MustCompile(`^[a-z]{2}$`)

func parseAIPage(data any) aiPage {
	d, _ := data.(map[string]any)
	p := aiPage{title: metaString(d["title"]), site: metaString(d["site_name"]), author: metaString(d["author"]),
		description: metaString(d["description"])}
	if b, ok := d["body"].(string); ok {
		p.body = strings.TrimSpace(sanitize(b))
	}
	p.kind, _ = d["page_kind"].(string)
	switch p.kind {
	case "article", "post", "login_wall", "error_page", "consent_or_paywall":
	default:
		p.kind = "other_content"
	}
	if s := metaString(d["published_at"]); s != nil {
		if t := parseTime(*s); t != "" {
			p.published = &t
		}
	}
	if s := metaString(d["lang"]); s != nil {
		if l := strings.ToLower(*s); langRe.MatchString(l) {
			p.lang = &l
		}
	}
	// A canonical URL cut at the cap would point elsewhere, so an over-long one is dropped.
	if s, _ := d["canonical_url"].(string); utf8.RuneCountInString(s) <= maxMetaChars {
		if c := metaString(s); c != nil {
			p.canonical = absHTTP(*c)
		}
	}
	return p
}

// markdownText is the text of a Markdown run: the data string, or its markdown member.
func markdownText(data any) string {
	s, ok := data.(string)
	if m, isMap := data.(map[string]any); isMap && !ok {
		s, _ = m["markdown"].(string)
	}
	return strings.TrimSpace(sanitize(stripMarkdown(s)))
}

// scrapeAIStudio runs the JSON extraction and, when needed, the Markdown fallback (at most two runs).
func (m *Module) scrapeAIStudio(ctx context.Context, u *url.URL) (*ss.Result, error) {
	a := &aiStudio{m: m, base: strings.TrimRight(orDefault(m.AIStudioURL, orDefault(m.cfg.AIStudioURL, ss.DefaultAIStudioURL)), "/")}
	target := u.String()
	min := m.cfg.MinTextChars

	var jp *aiPage
	data, rerr := a.run(ctx, target, true)
	if rerr == nil {
		p := parseAIPage(data)
		jp = &p
		switch {
		case p.kind == "login_wall":
			return nil, ss.LoginRequired(platform)
		case p.kind == "error_page":
			return nil, ss.NoTextContent(platform)
		case p.kind == "consent_or_paywall" && chars(p.body) < min:
			e := ss.LoginRequired(platform)
			e.Message = "the page is behind a consent or paywall and cannot be scraped"
			return nil, e
		case chars(p.body) >= min:
			return a.result(u, jp, p.body, "ai_studio_json"), nil
		}
	} else if rerr.fatal || ctx.Err() != nil {
		return nil, rerr.err
	}

	// JSON run failed or its body is too short: one Markdown run.
	data, rerr = a.run(ctx, target, false)
	text, extraction := "", "markdown_fallback"
	if rerr == nil {
		text = markdownText(data)
	}
	if jp != nil && chars(text) <= chars(jp.body) { // the short JSON body is the better text
		text, extraction = jp.body, "ai_studio_json"
	}
	switch {
	case text != "":
		return a.result(u, jp, text, extraction), nil
	case rerr != nil:
		return nil, rerr.err
	}
	return nil, ss.NoTextContent(platform)
}

// result maps the sanitized JSON page (nil when that run failed) and the chosen text to the page result.
func (a *aiStudio) result(u *url.URL, jp *aiPage, text, extraction string) *ss.Result {
	text, truncated := capText(text)
	host, domain := hostDomain(u)
	pg := &ss.Page{
		Text: text, RequestedURL: u.String(), FinalURL: u.String(), Host: host, Domain: domain,
		WordCount: len(strings.Fields(text)), Truncated: truncated, Extraction: extraction,
		Rendered: true, Provider: "oxylabs_ai_studio", OxylabsRequests: a.n,
	}
	if jp != nil {
		pg.Title, pg.SiteName, pg.Author, pg.Description = jp.title, jp.site, jp.author, jp.description
		pg.Lang, pg.CanonicalURL, pg.PublishedAt = jp.lang, jp.canonical, jp.published
	}
	return &ss.Result{Platform: platform, URLKind: ss.KindPage, Page: pg}
}
