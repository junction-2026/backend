package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/junction-2026/backend/internal/job"
)

const (
	maxSearchBody   = 8 << 20
	maxErrorSnippet = 200
	maxCacheEntries = 1024
)

// newsRetryBackoff is the pause before the single retry of a 429/5xx answer (a var for tests).
var newsRetryBackoff = time.Second

// provErr is a non-fatal provider failure (status 0: the request itself failed).
type provErr struct {
	status int
	msg    string
}

func (e *provErr) Error() string { return e.msg }

// serperResult is one news or web result as the provider returned it.
type serperResult struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
	Date    string `json:"date"`
	Source  string `json:"source"`
}

var headerValueRe = regexp.MustCompile(`(?i)(x-api-key|authorization)(["']?\s*[:=]\s*["']?)(bearer\s+)?[^\s"',;}]+`)

// redactor returns a function that removes the keys (also in JSON or URL escaped form) and header values.
func redactor(keys ...string) func(string) string {
	var pairs []string
	for _, k := range keys {
		if k == "" {
			continue
		}
		j, _ := json.Marshal(k)
		pairs = append(pairs, k, "[redacted]", strings.Trim(string(j), `"`), "[redacted]", url.QueryEscape(k), "[redacted]")
	}
	rep := strings.NewReplacer(pairs...)
	return func(s string) string {
		if len(pairs) > 0 {
			s = rep.Replace(s)
		}
		return headerValueRe.ReplaceAllString(s, "${1}${2}[redacted]")
	}
}

// post sends one JSON POST with the header, retrying a 429/5xx answer once after a backoff. Each request counts
// in searches_used when counted. Errors: *provErr, or the context's error.
func (r *newsRun) post(ctx context.Context, target, hdr, key string, body []byte, counted bool) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		b, status, err := r.once(ctx, target, hdr, key, body, counted)
		if err != nil {
			return nil, err
		}
		if status >= 200 && status <= 299 {
			return b, nil
		}
		if (status == http.StatusTooManyRequests || status >= 500) && attempt == 0 {
			select {
			case <-time.After(newsRetryBackoff):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		msg := "provider answered HTTP " + http.StatusText(status)
		if status < 100 || http.StatusText(status) == "" {
			msg = "provider answered an unexpected HTTP status"
		}
		if s := strings.Join(strings.Fields(string(b)), " "); s != "" && status != 401 && status != 403 {
			if len([]rune(s)) > maxErrorSnippet {
				s = string([]rune(s)[:maxErrorSnippet]) + "…"
			}
			msg += ": " + s
		}
		return nil, &provErr{status, r.redact(msg)}
	}
}

func (r *newsRun) once(ctx context.Context, target, hdr, key string, body []byte, counted bool) ([]byte, int, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	if ctx.Err() != nil { // select may pick the slot although the run is cancelled
		return nil, 0, ctx.Err()
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, 0, &provErr{0, "invalid request"}
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/json")
	hr.Header.Set(hdr, key)
	if counted {
		r.searches.Add(1)
	}
	resp, err := r.x.client().Do(hr)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, &provErr{0, "request to the provider failed"}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBody))
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, &provErr{0, "reading the provider answer failed"}
	}
	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && r.cancel != nil {
		r.cancel() // before the slot is released, so no queued search slips through
	}
	return b, resp.StatusCode, nil
}

func rejected(err error, who, setting string) error {
	var pe *provErr
	if errors.As(err, &pe) && (pe.status == http.StatusUnauthorized || pe.status == http.StatusForbidden) {
		return adapterErr("%s rejected the configured API key (%s)", who, setting)
	}
	return err
}

var gl = map[string]string{"en": "us", "de": "de", "fr": "fr"}

func countryFor(lang string) string {
	if c, ok := gl[lang]; ok {
		return c
	}
	return lang
}

// serper sends one search (endpoint news|search) through the cache. shared: answered without a request of
// this run (cache hit or another request in flight).
func (r *newsRun) serper(ctx context.Context, endpoint, q, lang string) (res []serperResult, shared bool, err error) {
	g := countryFor(lang)
	target := r.baseURL + "/" + endpoint
	res, shared, err = r.x.news.do(ctx, cacheKey(target, q, lang, g), r.cfg.CacheTTL, func() ([]serperResult, error) {
		body, _ := json.Marshal(map[string]any{"q": q, "hl": lang, "gl": g, "num": r.cfg.Num})
		b, err := r.post(ctx, target, "X-API-KEY", r.apiKey, body, true)
		if err != nil {
			return nil, rejected(err, "the search provider", "execution.api_key")
		}
		var out struct {
			News    []serperResult `json:"news"`
			Organic []serperResult `json:"organic"`
		}
		if json.Unmarshal(b, &out) != nil {
			return nil, &provErr{0, "the search provider answer is not a JSON object"}
		}
		if endpoint == "news" {
			return append([]serperResult{}, out.News...), nil
		}
		return append([]serperResult{}, out.Organic...), nil
	})
	if shared && err == nil {
		r.cached.Add(1)
	}
	return res, shared, err
}

func cacheKey(a, b, c, d string) string { return strings.Join([]string{a, b, c, d}, "\x00") }

// searchQuery runs the news search of one entry and, when it finds fewer than min_results, one web search.
// It returns only errors that abort the run (rejected key); other failures are recorded on the entry.
func (r *newsRun) searchQuery(ctx context.Context, q *searchQuery) error {
	news, shared, err := r.serper(ctx, "news", q.Q, q.Language)
	if err != nil {
		return r.recordFailure(ctx, q, "search_failed", err)
	}
	q.Cached, q.Results = shared, len(news)
	q.found = rankResults(news, q, r.now)
	if len(news) >= r.cfg.MinResults {
		return nil
	}
	web, shared2, err := r.serper(ctx, "search", q.Q, q.Language)
	if err != nil {
		q.Cached = false
		return r.recordFailure(ctx, nil, "search_failed", err) // best effort: the entry keeps its news results
	}
	q.Endpoint, q.Cached, q.Results = "search", shared && shared2, len(news)+len(web)
	q.found = append(q.found, rankResults(web, q, r.now)...)
	return nil
}

// recordFailure fails q (when given) unless err aborts the run, which it returns.
func (r *newsRun) recordFailure(ctx context.Context, q *searchQuery, code string, err error) error {
	var re *job.RunError
	if errors.As(err, &re) || ctx.Err() != nil {
		return err
	}
	if q != nil {
		q.fail(code, r.redact(err.Error()))
	}
	return nil
}

// newsCache holds successful provider answers (and the requests in flight) of this process.
type newsCache struct {
	mu sync.Mutex
	m  map[string]*cacheEntry
}

type cacheEntry struct {
	done    chan struct{}
	res     []serperResult
	err     error
	expires time.Time
}

// do returns the cached answer for key, shares an identical request in flight, or runs fn and caches its
// success for ttl. Failures are never cached.
func (c *newsCache) do(ctx context.Context, key string, ttl time.Duration, fn func() ([]serperResult, error)) ([]serperResult, bool, error) {
	for {
		c.mu.Lock()
		if c.m == nil {
			c.m = map[string]*cacheEntry{}
		}
		if ent := c.m[key]; ent != nil {
			select {
			case <-ent.done:
				if ent.err == nil && time.Now().Before(ent.expires) {
					c.mu.Unlock()
					return ent.res, true, nil
				}
			default: // in flight: wait for it
				c.mu.Unlock()
				select {
				case <-ent.done:
				case <-ctx.Done():
					return nil, false, ctx.Err()
				}
				if ent.err == nil {
					return ent.res, true, nil
				}
				if ctx.Err() == nil && (errors.Is(ent.err, context.Canceled) || errors.Is(ent.err, context.DeadlineExceeded)) {
					continue // the leader's job was cancelled, not ours: ask again
				}
				return nil, false, ent.err
			}
		}
		ent := &cacheEntry{done: make(chan struct{})}
		c.m[key] = ent
		if len(c.m) > maxCacheEntries { // shortcut: prune expired entries only; upgrade to an LRU if a deployment outgrows this
			for k, e := range c.m {
				select {
				case <-e.done:
					if e.err != nil || !time.Now().Before(e.expires) {
						delete(c.m, k)
					}
				default:
				}
			}
		}
		c.mu.Unlock()

		ent.err = errors.New("search did not complete") // replaced below; seen only if fn panics
		defer func() {
			c.mu.Lock()
			if ent.err != nil && c.m[key] == ent {
				delete(c.m, key)
			}
			ent.expires = time.Now().Add(ttl)
			c.mu.Unlock()
			close(ent.done)
		}()
		res, err := fn()
		ent.res, ent.err = res, err
		return res, false, err
	}
}
