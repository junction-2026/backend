package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
)

// Defaults of the public rate limits (requests per minute, process-wide).
const (
	DefaultPublicSubmitPerMinute = 30
	DefaultPublicGetPerMinute    = 600
	maxPublicComments            = 100
	publicPath                   = basePath + "/public/analyses"
)

// bucket is one process-wide token bucket: capacity perMinute, refill perMinute/60 tokens per second.
type bucket struct {
	mu           sync.Mutex
	rate, tokens float64
	burst        float64
	last         time.Time
	now          func() time.Time
}

func newBucket(perMinute int, def int, now func() time.Time) *bucket {
	if perMinute <= 0 {
		perMinute = def
	}
	return &bucket{rate: float64(perMinute) / 60, burst: float64(perMinute), tokens: float64(perMinute), last: now(), now: now}
}

// take consumes one token; otherwise it returns the seconds until one is available (at least 1).
func (b *bucket) take() (retryAfter int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.tokens = math.Min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return max(1, int(math.Ceil((1-b.tokens)/b.rate)))
	}
	b.tokens--
	return 0
}

// limited answers 429 rate_limited when b has no token.
func limited(w http.ResponseWriter, b *bucket) bool {
	retry := b.take()
	if retry == 0 {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests to the public API", map[string]any{"retry_after_s": retry})
	return true
}

// parsePublic validates a public submit body (an object) into a CreateRequest. ok is false when it has no content.
func parsePublic(top map[string]json.RawMessage) (req engine.CreateRequest, f fields, ok bool) {
	f = fields{}
	post, _ := f.str(top, "post", "post")
	text, _ := f.str(top, "text", "text")
	if _, p := member(top, "post"); p {
		if _, t := member(top, "text"); t && f["text"] == nil {
			f["text"] = "post and text must not both be present"
		}
	} else {
		post = text
	}

	var comments []string
	if raw, present := member(top, "comments"); present {
		var s string
		var elems []json.RawMessage
		switch {
		case json.Unmarshal(raw, &s) == nil:
			comments = []string{s}
		case json.Unmarshal(raw, &elems) != nil:
			f["comments"] = "must be a string or an array of strings"
		case len(elems) > maxPublicComments:
			f["comments"] = fmt.Sprintf("must have at most %d elements", maxPublicComments)
		default:
			for i, el := range elems {
				var c *string // nil for a null element
				if json.Unmarshal(el, &c) != nil || c == nil {
					f[fmt.Sprintf("comments[%d]", i)] = "must be a string"
					continue
				}
				comments = append(comments, *c)
			}
		}
	}
	var kept []string
	for _, c := range comments {
		if c = strings.TrimSpace(c); c != "" {
			kept = append(kept, c)
		}
	}
	hasPost := strings.TrimSpace(post) != ""

	link, hasURL := f.str(top, "url", "url")
	if hasURL {
		if u, err := url.Parse(link); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(link) > 2048 {
			f["url"] = "must be an absolute http(s) URL of at most 2048 characters"
		} else if hasPost || len(kept) > 0 {
			f["url"] = "must not be combined with post, text or comments"
		}
	}

	if raw, present := member(top, "public"); present {
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			f["public"] = "must be a boolean"
		}
		req.Private = !b
	}
	req.Finalize = new(true)

	switch {
	case hasURL:
		req.Scrape = &job.ScrapeRequest{URL: link, MaxPosts: 20, MaxComments: 50}
	case hasPost && len(kept) == 0:
		req.Input.Content = post // verbatim: analyses like the same text pasted in the UI
	case hasPost || len(kept) > 0:
		var blocks []string
		if hasPost {
			blocks = append(blocks, "Post:\n"+strings.TrimSpace(post))
		}
		for i, c := range kept {
			blocks = append(blocks, fmt.Sprintf("Comment %d:\n%s", i+1, c))
		}
		req.Input.Content = strings.Join(blocks, "\n\n")
	default:
		return req, f, false
	}
	if req.Scrape == nil {
		req.Input.ContentType = "text/plain"
		req.Input.Source.Metadata, _ = json.Marshal(map[string]any{"public_api": map[string]any{"has_post": hasPost, "comment_count": len(kept)}})
	}
	return req, f, true
}

func (a *api) submitPublic(w http.ResponseWriter, r *http.Request) {
	if limited(w, a.submitLimit) {
		return
	}
	raw, ok := readJSON(w, r)
	if !ok {
		return
	}
	top, ok := object(raw)
	if !ok {
		WriteError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object", nil)
		return
	}
	req, f, hasContent := parsePublic(top)
	switch {
	case len(f) > 0:
		WriteError(w, http.StatusUnprocessableEntity, "validation_failed", "request body violates the schema", map[string]any{"fields": f})
		return
	case !hasContent:
		WriteError(w, http.StatusBadRequest, "missing_content", "post, text, comments or url is required", nil)
		return
	}
	j, err := a.svc.Create(req)
	switch {
	case errors.Is(err, engine.ErrScrapeUnavailable):
		WriteError(w, http.StatusServiceUnavailable, "scrape_unavailable", "no scraper is configured on this server", nil)
	case err != nil:
		a.internal(w, "create public analysis", err)
	default:
		a.log.Info("job created", "job_id", j.ID)
		go a.logJob(j.ID)
		w.Header().Set("Location", publicPath+"/"+j.ID)
		writeJSON(w, http.StatusCreated, a.publicAnalysis(j))
	}
}

func (a *api) getPublic(w http.ResponseWriter, r *http.Request) {
	if limited(w, a.getLimit) {
		return
	}
	j, err := a.svc.Get(r.PathValue("id"))
	if err != nil {
		a.jobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.publicAnalysis(j))
}

// jsonObj is a JSON object read member by member; pick copies members unchanged, absent ones as null.
type jsonObj = map[string]json.RawMessage

func pick(m jsonObj, keys ...string) jsonObj {
	out := jsonObj{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		} else {
			out[k] = json.RawMessage("null")
		}
	}
	return out
}

type publicAnalysis struct {
	ID                 string            `json:"id"`
	Status             string            `json:"status"`
	Done               bool              `json:"done"`
	CreatedAt          time.Time         `json:"created_at"`
	FinishedAt         *time.Time        `json:"finished_at"`
	Coverage           job.Coverage      `json:"coverage"`
	Input              publicInput       `json:"input"`
	FinalizationStatus string            `json:"finalization_status"`
	Result             jsonObj           `json:"result"`
	Techniques         []publicTechnique `json:"techniques"`
	BotSignal          jsonObj           `json:"bot_signal"`
	Evidence           publicEvidence    `json:"evidence"`
	Error              *job.RunError     `json:"error"`
	ShareURL           *string           `json:"share_url"`
}

type publicInput struct {
	Content *string `json:"content"`
	URL     *string `json:"url"`
}

type publicTechnique struct {
	Source   string       `json:"source"`
	Label    string       `json:"label"`
	Name     string       `json:"name"`
	Score    *float64     `json:"score"`
	Strength *string      `json:"strength"`
	Spans    []publicSpan `json:"spans"`
}

type publicSpan struct {
	Field string `json:"field"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

type publicEvidence struct {
	FactChecks []jsonObj `json:"fact_checks"`
	News       []jsonObj `json:"news"`
	Wikipedia  []jsonObj `json:"wikipedia"`
}

// publicAnalysis maps the stored job only; it calls nothing.
func (a *api) publicAnalysis(j job.Job) publicAnalysis {
	p := publicAnalysis{ID: j.ID, Status: j.Status, Done: j.Done, CreatedAt: j.CreatedAt, FinishedAt: j.FinishedAt,
		Coverage: j.Coverage, FinalizationStatus: j.Finalization.Status}
	if j.Input != nil {
		p.Input.Content = &j.Input.Content
	}
	if j.Scrape != nil {
		p.Input.URL = &j.Scrape.URL
	}
	var m jsonObj
	if j.Finalization.Status == job.StatusCompleted && json.Unmarshal(j.Finalization.Result, &m) == nil && m != nil {
		p.Result = pick(m, "verdict", "confidence", "user_text", "produced_by")
	}
	runs := map[string]json.RawMessage{}
	for _, r := range j.Analyzers {
		if r.Status == job.RunSucceeded {
			runs[r.ID] = r.Result
		}
	}
	p.Techniques = techniques(runs)
	p.BotSignal = botSignal(runs)
	p.Evidence = evidence(runs)
	if j.Status == job.StatusFailed {
		if j.Scrape != nil && j.Scrape.Error != nil {
			p.Error = &job.RunError{Code: j.Scrape.Error.Code, Message: j.Scrape.Error.Message}
		} else {
			p.Error = &job.RunError{Code: "internal_error", Message: "the analysis failed"}
		}
	}
	if a.baseURL != "" {
		s := strings.TrimRight(a.baseURL, "/") + "/jobs/" + url.PathEscape(j.ID)
		p.ShareURL = &s
	}
	return p
}

func techniques(runs map[string]json.RawMessage) []publicTechnique {
	tcRaw, tcOK := runs["techniques"]
	llmRaw, llmOK := runs["llm_techniques"]
	if !tcOK && !llmOK {
		return nil
	}
	out := []publicTechnique{}
	var tc struct {
		Results []struct {
			Model  string `json:"model"`
			Scores []struct {
				Label string  `json:"label"`
				Score float64 `json:"score"`
			} `json:"scores"`
		} `json:"results"`
		Detected []struct {
			Name     string   `json:"name"`
			Display  string   `json:"display"`
			Models   []string `json:"models"`
			Strength *string  `json:"strength"`
			Spans    []struct {
				Text  string `json:"text"`
				Start int    `json:"start"`
				End   int    `json:"end"`
			} `json:"spans"`
		} `json:"detected"`
	}
	if tcOK {
		_ = json.Unmarshal(tcRaw, &tc) // a malformed result adds nothing
	}
	for _, d := range tc.Detected {
		t := publicTechnique{Source: "classifier", Label: d.Name, Name: d.Display, Strength: d.Strength, Spans: []publicSpan{}}
		for _, mr := range tc.Results {
			for _, model := range d.Models {
				if model != mr.Model {
					continue
				}
				for _, s := range mr.Scores {
					if s.Label == d.Name && (t.Score == nil || s.Score > *t.Score) {
						t.Score = &s.Score
					}
				}
			}
		}
		for _, s := range d.Spans {
			t.Spans = append(t.Spans, publicSpan{Field: "content", Start: s.Start, End: s.End, Text: s.Text})
		}
		out = append(out, t)
	}
	var llm struct {
		Findings []struct {
			Label      string     `json:"label"`
			Name       string     `json:"name"`
			Confidence *float64   `json:"confidence"`
			Span       publicSpan `json:"span"`
		} `json:"findings"`
	}
	if llmOK {
		_ = json.Unmarshal(llmRaw, &llm)
	}
	for _, f := range llm.Findings {
		out = append(out, publicTechnique{Source: "llm", Label: f.Label, Name: f.Name, Score: f.Confidence, Spans: []publicSpan{f.Span}})
	}
	return out
}

// botSignal is the TextBotDetection when clients may read it (bot-detection-result "Text scope is not an account
// verdict"), else nil.
func botSignal(runs map[string]json.RawMessage) jsonObj {
	var b struct {
		Label *string  `json:"label"`
		Score *float64 `json:"score"`
		Scope *string  `json:"scope"`
	}
	r, ok := runs["bot_detection"]
	if !ok || json.Unmarshal(r, &b) != nil || b.Label == nil || b.Score == nil || b.Scope == nil ||
		*b.Scope != "text" || (*b.Label != "human" && *b.Label != "bot_or_generated") || *b.Score < 0 || *b.Score > 1 {
		return nil
	}
	var m jsonObj
	_ = json.Unmarshal(r, &m)
	return pick(m, "label", "score", "scope")
}

func evidence(runs map[string]json.RawMessage) publicEvidence {
	var e publicEvidence
	if r, ok := runs["fact_check"]; ok {
		var fc struct {
			FactChecks []jsonObj `json:"fact_checks"`
		}
		_ = json.Unmarshal(r, &fc)
		e.FactChecks = []jsonObj{}
		for _, c := range fc.FactChecks {
			e.FactChecks = append(e.FactChecks, pick(c, "claim", "for_claim", "rating", "rating_normalized", "publisher", "url", "review_date"))
		}
	}
	if r, ok := runs["news_search"]; ok {
		var ns struct {
			Articles []jsonObj `json:"articles"`
		}
		_ = json.Unmarshal(r, &ns)
		e.News = []jsonObj{}
		for _, a := range ns.Articles {
			e.News = append(e.News, pick(a, "title", "url", "domain", "source", "date"))
		}
	}
	if r, ok := runs["wikipedia"]; ok {
		var wl struct {
			Claims []struct {
				Claim json.RawMessage `json:"claim"`
				Wikis []jsonObj       `json:"wikis"`
			} `json:"claims"`
		}
		_ = json.Unmarshal(r, &wl)
		e.Wikipedia = []jsonObj{}
		for _, c := range wl.Claims {
			for _, w := range c.Wikis {
				var status string
				if json.Unmarshal(w["status"], &status) != nil || status != "found" {
					continue
				}
				entry := pick(w, "language", "title", "url", "passage", "coverage")
				entry["claim"] = c.Claim
				if c.Claim == nil {
					entry["claim"] = json.RawMessage("null")
				}
				e.Wikipedia = append(e.Wikipedia, entry)
			}
		}
	}
	return e
}
