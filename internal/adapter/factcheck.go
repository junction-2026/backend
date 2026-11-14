package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// Skip reason of fact_check (shared capability fact-check-result).
const ReasonNoCheckableClaims = "no_checkable_claims"

// maxCachedAnswer is the largest provider answer (bytes) worth keeping in memory.
const maxCachedAnswer = 256 << 10

// factBackoff is the wait before the single retry of a 429/5xx (a var for tests).
var factBackoff = time.Second

// factCache holds successful provider answers per (languageCode, query).
type factCache struct {
	mu sync.Mutex
	m  map[string]factCacheEntry
}

type factCacheEntry struct {
	claims  []providerClaim
	expires time.Time
}

func (c *factCache) get(key string) ([]providerClaim, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if ok && time.Now().After(e.expires) {
		delete(c.m, key)
		return nil, false
	}
	return e.claims, ok
}

func (c *factCache) put(key string, claims []providerClaim, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]factCacheEntry{}
	}
	if _, ok := c.m[key]; !ok && len(c.m) >= maxCacheEntries { // hard cap: drop expired, then the soonest-expiring
		now, oldest := time.Now(), ""
		for k, e := range c.m {
			if now.After(e.expires) {
				delete(c.m, k)
			} else if oldest == "" || e.expires.Before(c.m[oldest].expires) {
				oldest = k
			}
		}
		if len(c.m) >= maxCacheEntries {
			delete(c.m, oldest)
		}
	}
	c.m[key] = factCacheEntry{claims, time.Now().Add(ttl)}
}

// providerClaim is one entry of the Fact Check Tools `claims` array.
type providerClaim struct {
	Text        *string `json:"text"`
	Claimant    *string `json:"claimant"`
	ClaimDate   *string `json:"claimDate"`
	ClaimReview []struct {
		Publisher *struct {
			Name *string `json:"name"`
			Site *string `json:"site"`
		} `json:"publisher"`
		URL           string  `json:"url"`
		Title         *string `json:"title"`
		ReviewDate    *string `json:"reviewDate"`
		TextualRating *string `json:"textualRating"`
		LanguageCode  *string `json:"languageCode"`
	} `json:"claimReview"`
}

type plannedQuery struct{ q, lang, forClaim string }

type factQuery struct {
	Q        string    `json:"q"`
	Language string    `json:"language"`
	ForClaim string    `json:"for_claim"`
	Claims   int       `json:"claims"`
	Status   string    `json:"status"`
	Cached   bool      `json:"cached"`
	Error    *queryErr `json:"error"`
}

type queryErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type factCheckItem struct {
	ForClaim         string  `json:"for_claim"`
	Query            string  `json:"query"`
	Claim            *string `json:"claim"`
	Claimant         *string `json:"claimant"`
	ClaimDate        *string `json:"claim_date"`
	Publisher        *string `json:"publisher"`
	Site             *string `json:"site"`
	URL              string  `json:"url"`
	Title            *string `json:"title"`
	Rating           *string `json:"rating"`
	RatingNormalized string  `json:"rating_normalized"`
	Language         *string `json:"language"`
	ReviewDate       *string `json:"review_date"`
}

// factCheck looks up the claims of the topic_extraction result (binding
// `extraction`) in Google Fact Check Tools. Errors never contain the API key.
func (x *Executor) factCheck(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	e := &def.Execution
	raw, _ := json.Marshal(req.Inputs["extraction"])
	var ext map[string]json.RawMessage
	if json.Unmarshal(raw, &ext) != nil || ext == nil {
		return nil, adapterErr("topic_extraction result is not a JSON object")
	}
	plan := planFactChecks(ext["claims"], e.MinWorthiness(), e.FactCheckMaxClaims())
	if len(plan) == 0 {
		return nil, &Skip{Reason: ReasonNoCheckableClaims}
	}

	f := &factRun{x: x, e: e, redact: []string{e.APIKey, url.QueryEscape(e.APIKey)}}
	var fatal *job.RunError
	var fatalOnce sync.Once
	fctx, cancel := context.WithCancel(ctx)
	defer cancel()
	f.fctx = fctx
	f.setFatal = func(err *job.RunError) { fatalOnce.Do(func() { fatal = err; cancel() }) }

	answers := make([][]providerClaim, len(plan))
	queries := make([]factQuery, len(plan))
	sem := make(chan struct{}, e.MaxConcurrencyN())
	var wg sync.WaitGroup
	for i, p := range plan {
		wg.Add(1)
		go func() {
			defer wg.Done()
			queries[i] = factQuery{Q: p.q, Language: p.lang, ForClaim: p.forClaim, Status: "failed"}
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-fctx.Done():
				return
			}
			if fctx.Err() != nil { // a rejected key or the deadline arrived while queued
				return
			}
			answers[i] = f.search(p, &queries[i])
		}()
	}
	wg.Wait()
	if err := ContextError(ctx); err != nil {
		return nil, err
	}
	if fatal != nil {
		return nil, fatal
	}

	succeeded, cached := 0, 0
	for _, q := range queries {
		if q.Status == "succeeded" {
			succeeded++
		}
		if q.Cached {
			cached++
		}
	}
	if succeeded == 0 {
		return nil, adapterErr("every fact check search failed")
	}
	out, err := json.Marshal(map[string]any{
		"queries": queries, "fact_checks": assembleFactChecks(plan, queries, answers, e.MaxResultsN()),
		"requests_used": int(f.requests.Load()), "cached": cached,
	})
	if err != nil {
		return nil, adapterErr("fact check result is not serializable")
	}
	return out, nil
}

// planFactChecks applies the threshold and cap and returns the deduplicated
// searches in claims order (a claim's query before its query_en).
func planFactChecks(rawClaims json.RawMessage, minWorth float64, maxClaims int) []plannedQuery {
	var claims []claim
	if json.Unmarshal(rawClaims, &claims) != nil { // missing or malformed claims: nothing to check
		return nil
	}
	var plan []plannedQuery
	seen := map[[2]string]bool{}
	add := func(q, lang, text string) {
		if k := [2]string{q, lang}; strings.TrimSpace(q) != "" && !seen[k] {
			seen[k] = true
			plan = append(plan, plannedQuery{q, lang, text})
		}
	}
	taken := 0
	for _, c := range claims {
		if c.CheckWorthiness < minWorth || taken >= maxClaims {
			continue
		}
		taken++
		add(c.Query, c.Language, c.Text)
		if !strings.EqualFold(strings.SplitN(c.Language, "-", 2)[0], "en") {
			add(c.QueryEN, "en", c.Text)
		}
	}
	return plan
}

type factRun struct {
	x        *Executor
	e        *registry.Execution
	fctx     context.Context // cancelled as well once the key was rejected
	redact   []string
	requests atomic.Int32
	setFatal func(*job.RunError)
}

func (f *factRun) clean(s string) string {
	for _, k := range f.redact {
		if len(k) >= 4 {
			s = strings.ReplaceAll(s, k, "[REDACTED]")
		}
	}
	return s
}

// search answers one planned query from the cache or the provider and fills q.
// It returns the provider claims of a succeeded query.
func (f *factRun) search(p plannedQuery, q *factQuery) []providerClaim {
	key := p.lang + "\x00" + p.q
	if c, ok := f.x.factCache.get(key); ok {
		q.Status, q.Cached, q.Claims = "succeeded", true, len(c)
		return c
	}
	fail := func(msg string) []providerClaim {
		q.Error = &queryErr{"search_failed", msg}
		return nil
	}
	for attempt := 0; ; attempt++ {
		status, body, retryAfter, err := f.get(p)
		if err != nil {
			return fail("fact check request failed") // net/http errors embed the URL, so none is formatted
		}
		switch {
		case status >= 200 && status < 300:
			var r struct {
				Claims []providerClaim `json:"claims"`
			}
			if json.Unmarshal(body, &r) != nil {
				return fail("fact check answer could not be decoded")
			}
			if len(body) <= maxCachedAnswer { // an oversized answer is served once, not kept
				f.x.factCache.put(key, r.Claims, f.e.CacheTTLD())
			}
			q.Status, q.Claims = "succeeded", len(r.Claims)
			return r.Claims
		case status == 400 || status == 401 || status == 403:
			f.setFatal(adapterErr("fact check API rejected the request (HTTP %d): the configured api_key is invalid or the Fact Check Tools API is not enabled for the key's Google Cloud project%s", status, f.providerDetail(body)))
			return nil
		case (status == 429 || status >= 500) && attempt == 0:
			t := time.NewTimer(retryAfter)
			select {
			case <-f.fctx.Done():
				t.Stop()
				return nil
			case <-t.C:
			}
		default:
			return fail("fact check provider HTTP " + strconv.Itoa(status))
		}
	}
}

// providerDetail is ": <provider message>" with the key redacted before truncation.
func (f *factRun) providerDetail(body []byte) string {
	var p struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(body, &p)
	if m := strings.TrimSpace(f.clean(p.Error.Message)); m != "" {
		return ": " + truncate(m, 200)
	}
	return ""
}

// get sends one request; retryAfter is the backoff before a retry.
func (f *factRun) get(p plannedQuery) (status int, body []byte, retryAfter time.Duration, err error) {
	v := url.Values{"query": {p.q}, "pageSize": {strconv.Itoa(f.e.PageSizeN())},
		"maxAgeDays": {strconv.Itoa(f.e.MaxAgeDaysN())}, "key": {f.e.APIKey}}
	if p.lang != "" {
		v.Set("languageCode", p.lang)
	}
	target := strings.TrimRight(f.e.BaseURL, "/") + "/claims:search?" + v.Encode()
	hr, err := http.NewRequestWithContext(f.fctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, 0, err
	}
	hr.Header.Set("Accept", "application/json")
	f.requests.Add(1)
	resp, err := f.x.client().Do(hr)
	if err != nil {
		return 0, nil, 0, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBody))
	retryAfter = factBackoff
	if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); resp.StatusCode == http.StatusTooManyRequests && perr == nil && s >= 0 {
		retryAfter = min(time.Duration(s)*time.Second, llmMaxRetryAfter)
	}
	return resp.StatusCode, body, retryAfter, err
}

// assembleFactChecks flattens, drops url-less and repeated reviews, sorts newest first and caps.
func assembleFactChecks(plan []plannedQuery, queries []factQuery, answers [][]providerClaim, limit int) []factCheckItem {
	items := []factCheckItem{}
	seen := map[string]bool{}
	for i, p := range plan {
		if queries[i].Status != "succeeded" {
			continue
		}
		for _, c := range answers[i] {
			for _, r := range c.ClaimReview {
				if strings.TrimSpace(r.URL) == "" || seen[r.URL] {
					continue
				}
				seen[r.URL] = true
				it := factCheckItem{ForClaim: p.forClaim, Query: p.q, Claim: c.Text, Claimant: c.Claimant, ClaimDate: c.ClaimDate,
					URL: r.URL, Title: r.Title, Rating: r.TextualRating, RatingNormalized: normalizeRating(r.TextualRating),
					Language: r.LanguageCode, ReviewDate: r.ReviewDate}
				if r.Publisher != nil {
					it.Publisher, it.Site = r.Publisher.Name, r.Publisher.Site
				}
				items = append(items, it)
			}
		}
	}
	sort.SliceStable(items, func(a, b int) bool {
		ta, oka := parseDate(items[a].ReviewDate)
		tb, okb := parseDate(items[b].ReviewDate)
		if oka != okb {
			return oka
		}
		return oka && ta.After(tb)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func parseDate(s *string) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, strings.TrimSpace(*s)); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ratingTerms maps lower-case textual ratings (English and major European
// languages) to rating_normalized; anything else is "unrated".
var ratingTerms = func() map[string]string {
	m := map[string]string{}
	for v, terms := range map[string][]string{
		"false": {"false", "falsch", "faux", "falso", "falsa", "fake", "incorrect", "wrong", "pants on fire", "unwahr",
			"incorrecto", "incorretto", "incorreto", "onwaar", "fałsz", "nieprawda", "falske", "falskt"},
		"misleading": {"misleading", "irreführend", "trompeur", "engañoso", "fuorviante", "enganoso", "misleidend", "mylące", "wprowadzające w błąd"},
		"mixed": {"partly true", "partially true", "partly false", "half true", "mixture", "mixed", "teilweise richtig",
			"teilweise wahr", "teilweise falsch", "en partie vrai", "partiellement vrai", "parcialmente verdadero",
			"parzialmente vero", "parcialmente verdadeiro", "deels waar", "częściowo prawda"},
		"true": {"true", "richtig", "wahr", "correct", "accurate", "vrai", "verdadero", "cierto", "vero", "verdadeiro", "waar", "prawda"},
	} {
		for _, t := range terms {
			m[t] = v
		}
	}
	return m
}()

func normalizeRating(r *string) string {
	if r == nil {
		return "unrated"
	}
	s := strings.TrimRightFunc(strings.TrimSpace(*r), func(c rune) bool { return unicode.IsPunct(c) || unicode.IsSpace(c) })
	if v, ok := ratingTerms[strings.ToLower(s)]; ok {
		return v
	}
	return "unrated"
}
