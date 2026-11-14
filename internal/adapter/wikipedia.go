package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// wikiUserAgentApp is the <app>/<version> part of the User-Agent (Wikimedia policy); the contact comes from execution.user_agent.
const wikiUserAgentApp = "JunctionOrchestrator/1.0"

// wikiBackoff is the wait before the single retry of a 429/5xx (a var for tests).
var wikiBackoff = time.Second

// wikiLookup is the shared WikiLookup object; the page members are null unless status is found.
type wikiLookup struct {
	Language         string   `json:"language"`
	Query            string   `json:"query"`
	Status           string   `json:"status"`
	Cached           bool     `json:"cached"`
	Title            *string  `json:"title"`
	URL              *string  `json:"url"`
	PageID           *int64   `json:"page_id"`
	RevisionID       *int64   `json:"revision_id"`
	RevisedAt        *string  `json:"revised_at"`
	Passage          *string  `json:"passage"`
	PassageSource    *string  `json:"passage_source"`
	MatchedEntities  []string `json:"matched_entities"`
	MatchedNumbers   []string `json:"matched_numbers"`
	Coverage         string   `json:"coverage"`
	EvidenceSentence *string  `json:"evidence_sentence"`
}

type claimLookup struct {
	Claim           string       `json:"claim"`
	CheckWorthiness float64      `json:"check_worthiness"`
	SearchMode      string       `json:"search_mode"`
	Wikis           []wikiLookup `json:"wikis"`
}

type wikiFlight struct {
	done   chan struct{}
	key    string
	l      wikiLookup
	failed bool
}

type wikiRun struct {
	ctx      context.Context // cancelled by the first failed request
	cfg      registry.Wikipedia
	x        *Executor
	cache    wikiCache
	sem      chan struct{} // requests in flight
	requests atomic.Int64
	setFatal func(error)
	mu       sync.Mutex
	flights  map[string]*wikiFlight // identical lookups of this run
}

// wikipedia looks up the qualifying claims of the bound topic_extraction result in the post's wiki and in English.
func (x *Executor) wikipedia(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	cfg := def.Execution.Wiki
	raw, _ := json.Marshal(req.Inputs["extraction"])
	var ext map[string]json.RawMessage
	if json.Unmarshal(raw, &ext) != nil || ext == nil {
		return nil, adapterErr("topic_extraction result is not a JSON object")
	}
	claims := qualifyingClaims(ext["claims"], cfg.MinCheckWorthiness, cfg.MaxClaims)
	if len(claims) == 0 {
		return nil, &Skip{Reason: ReasonNoCheckableClaims}
	}
	langs := []string{"en"}
	if src := readSourceLanguage(req.Inputs["translation"]); src != "" && src != "en" {
		langs = []string{src, "en"}
	}

	pctx := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var fatal error
	var once sync.Once
	r := &wikiRun{ctx: ctx, cfg: cfg, x: x, cache: wikiCache{cfg.CacheDir, cfg.CacheTTL}, sem: make(chan struct{}, cfg.MaxConcurrency),
		flights: map[string]*wikiFlight{}, setFatal: func(err error) {
			if _, ok := err.(*job.RunError); ok { // a context error is judged after wg.Wait
				once.Do(func() { fatal = err })
				cancel()
			}
		}}

	out := make([]claimLookup, len(claims))
	var wg sync.WaitGroup
	for i, c := range claims {
		out[i] = claimLookup{Claim: c.Text, CheckWorthiness: c.CheckWorthiness, SearchMode: searchMode(c), Wikis: make([]wikiLookup, len(langs))}
		for j, lang := range langs {
			q := c.QueryEN
			if lang != "en" {
				q = c.Query
			}
			f, leader := r.flight(wikiKey(lang, q, out[i].SearchMode)) // claim order decides which of identical lookups asks
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i].Wikis[j] = r.lookup(f, leader, c, out[i].SearchMode, lang, q)
			}()
		}
	}
	wg.Wait()
	if err := ContextError(pctx); err != nil {
		return nil, err
	}
	if fatal != nil {
		return nil, fatal
	}
	cached := 0
	for _, c := range out {
		for _, w := range c.Wikis {
			if w.Cached {
				cached++
			}
		}
	}
	b, err := json.Marshal(map[string]any{"languages": langs, "claims": out, "requests_used": r.requests.Load(), "cached": cached})
	if err != nil {
		return nil, adapterErr("wikipedia result is not serializable")
	}
	return b, nil
}

// qualifyingClaims applies the threshold and the cap, keeping the order of claims.
func qualifyingClaims(rawClaims json.RawMessage, minWorth float64, maxClaims int) []claim {
	var all, out []claim
	if json.Unmarshal(rawClaims, &all) != nil {
		return nil
	}
	for _, c := range all {
		if c.CheckWorthiness >= minWorth && len(out) < maxClaims {
			out = append(out, c)
		}
	}
	return out
}

// flight returns the run's flight for key; leader is true for the first lookup with that key.
func (r *wikiRun) flight(key string) (f *wikiFlight, leader bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f = r.flights[key]; f == nil {
		f, leader = &wikiFlight{done: make(chan struct{}), key: key}, true
		r.flights[key] = f
	}
	return f, leader
}

// lookup answers one WikiLookup from this run, the disk cache or Wikipedia. On a failure it records the
// fatal error and returns the zero value (the run then fails as a whole).
func (r *wikiRun) lookup(f *wikiFlight, leader bool, c claim, mode, lang, query string) wikiLookup {
	if !leader {
		select {
		case <-f.done:
		case <-r.ctx.Done():
			return wikiLookup{}
		}
		if f.failed {
			return wikiLookup{}
		}
		return personalize(f.l, c, query, true)
	}
	defer close(f.done)

	if l, ok := r.cache.get(f.key); ok {
		f.l = l
		return personalize(l, c, query, true)
	}
	l, err := r.fetch(c, mode, lang, query)
	if err != nil {
		f.failed = true
		r.setFatal(err)
		return wikiLookup{}
	}
	f.l = l
	r.cache.put(f.key, l)
	return personalize(l, c, query, false)
}

// personalize adapts a stored answer to this claim: the query as sent and the matches of this claim's entities and numbers.
func personalize(l wikiLookup, c claim, query string, cached bool) wikiLookup {
	l.Query, l.Cached = query, cached
	if l.Status == "found" {
		l.MatchedEntities, l.MatchedNumbers = matchedEntities(c.Entities, *l.Passage), matchedNumbers(c.Text, *l.Passage)
	}
	l.Coverage, l.EvidenceSentence = wikiCoverage(c, l)
	return l
}

type wikiHit struct {
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

type wikiPage struct {
	PageID    int64                      `json:"pageid"`
	Title     string                     `json:"title"`
	Missing   bool                       `json:"missing"`
	PageProps map[string]json.RawMessage `json:"pageprops"`
	Revisions []struct {
		RevID     int64  `json:"revid"`
		Timestamp string `json:"timestamp"`
	} `json:"revisions"`
	Extract string `json:"extract"`
}

type wikiMap struct{ From, To string }

// fetch runs the search and, when there are hits, the page request, and selects the page.
func (r *wikiRun) fetch(c claim, mode, lang, query string) (wikiLookup, error) {
	l := wikiLookup{Language: lang, Query: query, Status: "no_page"}
	body, err := r.get(lang, url.Values{"action": {"query"}, "list": {"search"}, "srsearch": {query}, "srnamespace": {"0"},
		"srlimit": {strconv.Itoa(r.cfg.MaxHits)}, "srprop": {"snippet"}})
	if err != nil {
		return l, err
	}
	var sr struct {
		Query *struct {
			Search []wikiHit `json:"search"`
		} `json:"query"`
	}
	if json.Unmarshal(body, &sr) != nil || sr.Query == nil {
		return l, wikiErr(lang, "answer could not be decoded")
	}
	hits := sr.Query.Search
	if len(hits) == 0 {
		return l, nil
	}
	titles := make([]string, len(hits))
	for i, h := range hits {
		titles[i] = h.Title
	}
	body, err = r.get(lang, url.Values{"action": {"query"}, "prop": {"pageprops|revisions|extracts"}, "ppprop": {"disambiguation"},
		"rvprop": {"ids|timestamp"}, "exintro": {"1"}, "explaintext": {"1"}, "exlimit": {"max"}, "redirects": {"1"},
		"titles": {strings.Join(titles, "|")}})
	if err != nil {
		return l, err
	}
	var pr struct {
		Query *struct {
			Normalized []wikiMap  `json:"normalized"`
			Redirects  []wikiMap  `json:"redirects"`
			Pages      []wikiPage `json:"pages"`
		} `json:"query"`
	}
	if json.Unmarshal(body, &pr) != nil || pr.Query == nil {
		return l, wikiErr(lang, "answer could not be decoded")
	}
	to := map[string]string{}
	for _, m := range append(pr.Query.Normalized, pr.Query.Redirects...) {
		to[m.From] = m.To
	}
	byTitle := map[string]*wikiPage{}
	for i, p := range pr.Query.Pages {
		byTitle[p.Title] = &pr.Query.Pages[i]
	}
	type cand struct {
		hit  wikiHit
		page *wikiPage
	}
	var cands []cand
	for _, h := range hits {
		t := h.Title
		for range 3 { // normalized, then redirected
			if n, ok := to[t]; ok {
				t = n
			}
		}
		if p := byTitle[t]; p != nil && !p.Missing {
			cands = append(cands, cand{h, p})
		}
	}
	if len(cands) == 0 {
		return l, nil
	}
	disambiguation := func(p *wikiPage) bool { _, ok := p.PageProps["disambiguation"]; return ok }
	firstEntity := func() *cand {
		for i, c2 := range cands {
			if !disambiguation(c2.page) && entityTitle(c2.hit.Title, c.Entities) {
				return &cands[i]
			}
		}
		return nil
	}
	chosen := &cands[0]
	if m := firstEntity(); mode == "title" && m != nil {
		chosen = m
	}
	if disambiguation(chosen.page) {
		if chosen = firstEntity(); chosen == nil {
			l.Status = "ambiguous"
			return l, nil
		}
	}

	p := chosen.page
	snippet, lead := snippetText(chosen.hit.Snippet), cutRunes(p.Extract, r.cfg.MaxPassageChars)
	passage, source := lead, "lead"
	if mode == "fulltext" && strings.TrimSpace(snippet) != "" || strings.TrimSpace(lead) == "" {
		passage, source = snippet, "snippet"
	}
	if strings.TrimSpace(passage) == "" { // Wikipedia returned no text: nothing to show
		return l, nil
	}
	if len(p.Revisions) == 0 {
		return l, wikiErr(lang, "answer could not be decoded")
	}
	at, err := time.Parse(time.RFC3339, p.Revisions[0].Timestamp)
	if err != nil || p.PageID < 1 || p.Revisions[0].RevID < 1 {
		return l, wikiErr(lang, "answer could not be decoded")
	}
	rev := p.Revisions[0].RevID
	link := fmt.Sprintf("https://%s.wikipedia.org/w/index.php?title=%s&oldid=%d", lang, url.QueryEscape(strings.ReplaceAll(p.Title, " ", "_")), rev)
	revised := at.UTC().Format(time.RFC3339)
	l.Status, l.Title, l.URL, l.PageID, l.RevisionID, l.RevisedAt = "found", &p.Title, &link, &p.PageID, &rev, &revised
	l.Passage, l.PassageSource = &passage, &source
	return personalize(l, c, query, false), nil
}

func wikiErr(lang, what string) *job.RunError {
	return adapterErr("%s.wikipedia.org: %s", lang, what)
}

// get sends one MediaWiki API request (format=json, formatversion=2), retrying a 429/5xx once. Errors name the
// wiki and the HTTP status but never a URL.
func (r *wikiRun) get(lang string, v url.Values) ([]byte, error) {
	v.Set("format", "json")
	v.Set("formatversion", "2")
	target := strings.ReplaceAll(r.cfg.BaseURLTemplate, "{lang}", lang)
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	target += sep + v.Encode()
	for attempt := 0; ; attempt++ {
		select {
		case r.sem <- struct{}{}:
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		}
		status, body, retryAfter, err := r.do(target)
		<-r.sem
		switch {
		case r.ctx.Err() != nil:
			return nil, r.ctx.Err()
		case err != nil:
			return nil, wikiErr(lang, "request failed")
		case status >= 200 && status < 300:
			return body, nil
		case (status == 429 || status >= 500) && attempt == 0:
			t := time.NewTimer(retryAfter)
			select {
			case <-r.ctx.Done():
				t.Stop()
				return nil, r.ctx.Err()
			case <-t.C:
			}
		default:
			return nil, wikiErr(lang, "answered HTTP "+strconv.Itoa(status))
		}
	}
}

func (r *wikiRun) do(target string) (status int, body []byte, retryAfter time.Duration, err error) {
	hr, err := http.NewRequestWithContext(r.ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, 0, err
	}
	hr.Header.Set("Accept", "application/json")
	hr.Header.Set("User-Agent", wikiUserAgentApp+" ("+r.cfg.UserAgent+")")
	r.requests.Add(1)
	resp, err := r.x.client().Do(hr)
	if err != nil {
		return 0, nil, 0, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBody))
	retryAfter = wikiBackoff
	if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); resp.StatusCode == http.StatusTooManyRequests && perr == nil && s >= 0 {
		retryAfter = min(time.Duration(s)*time.Second, llmMaxRetryAfter)
	}
	return resp.StatusCode, body, retryAfter, err
}
