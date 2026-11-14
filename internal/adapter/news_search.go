package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

var langCodeRe = regexp.MustCompile(`^[a-z]{2}$`)

// SearchQuery is one query in one language; its json form is the shared SearchQuery object.
type searchQuery struct {
	Q        string      `json:"q"`
	Original string      `json:"original"`
	Origin   string      `json:"origin"`
	Language string      `json:"language"`
	Endpoint string      `json:"endpoint"`
	Status   string      `json:"status"`
	Results  int         `json:"results"`
	Cached   bool        `json:"cached"`
	Error    *queryError `json:"error"`

	idx   int
	found []foundResult
}

type queryError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (q *searchQuery) fail(code, msg string) {
	q.Status, q.Results, q.Cached, q.found = "failed", 0, false, nil
	q.Error = &queryError{code, msg}
}

// newsRun is the state of one news_search run.
type newsRun struct {
	x        *Executor
	cfg      registry.NewsSearch
	baseURL  string
	apiKey   string
	sem      chan struct{} // in-flight provider requests
	searches atomic.Int64
	cached   atomic.Int64
	redact   func(string) string
	now      time.Time
	cancel   context.CancelFunc // cancels the run's remaining requests
}

// newsSearch searches news and web coverage of the topics of the bound topic_extraction result.
func (x *Executor) newsSearch(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	e := def.Execution
	r := &newsRun{x: x, cfg: e.News, baseURL: strings.TrimRight(e.BaseURL, "/"), apiKey: e.APIKey,
		sem: make(chan struct{}, max(e.News.MaxConcurrency, 1)), redact: redactor(e.APIKey, e.News.Translate.APIKey), now: time.Now()}
	topics, news, err := readTopics(req.Inputs["topics"])
	if err != nil {
		return nil, adapterErr("%v", err)
	}
	source := readSourceLanguage(req.Inputs["translation"])
	languages := resolveLanguages(r.cfg.Languages, source)
	if len(languages) == 0 && len(topics)+len(news) > 0 {
		return nil, adapterErr("no search language: the source language is unknown and execution.languages has only source")
	}
	queries := buildQueries(r.cfg, topics, news, languages)
	if len(queries) == 0 {
		return nil, &Skip{Reason: ReasonNoSearchableTopics}
	}

	// the first rejected key cancels the remaining requests
	pctx := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var once sync.Once
	var fatal *job.RunError
	r.cancel = cancel
	setFatal := func(err error) { // only a rejected key aborts; a context error is judged after wg.Wait
		if re, ok := err.(*job.RunError); ok {
			once.Do(func() { fatal = re })
			cancel()
		}
	}

	// one translation request per language with queries that need it
	ready := map[string]chan struct{}{}
	var wg sync.WaitGroup
	for _, lang := range languages {
		done := make(chan struct{})
		ready[lang] = done
		var todo []*searchQuery
		if source == "" || lang != source {
			for _, q := range queries {
				if q.Language == lang {
					todo = append(todo, q)
				}
			}
		} else {
			for _, q := range queries {
				if q.Language == lang {
					q.Q = q.Original
				}
			}
		}
		if len(todo) == 0 {
			close(done)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(done)
			if err := r.translateAll(ctx, lang, todo); err != nil {
				setFatal(err)
			}
		}()
	}
	for _, q := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ready[q.Language]:
			case <-ctx.Done():
				return
			}
			if q.Status == "failed" || ctx.Err() != nil {
				return
			}
			if err := r.searchQuery(ctx, q); err != nil {
				setFatal(err)
			}
		}()
	}
	wg.Wait()
	// a rejected key beats the context error its cancellation caused
	if fatal != nil {
		return nil, fatal
	}
	if ce := ContextError(pctx); ce != nil {
		return nil, ce
	}
	ok := 0
	for _, q := range queries {
		if q.Status == "succeeded" {
			ok++
		}
	}
	if ok == 0 {
		return nil, adapterErr("every search failed (%d queries); first error: %s", len(queries), r.redact(queries[0].Error.Message))
	}
	res := struct {
		Languages    []string       `json:"languages"`
		Queries      []*searchQuery `json:"queries"`
		Articles     []article      `json:"articles"`
		SearchesUsed int64          `json:"searches_used"`
		Cached       int64          `json:"cached"`
	}{languages, queries, mergeArticles(r.cfg, queries), r.searches.Load(), r.cached.Load()}
	out, err := json.Marshal(res)
	if err != nil {
		return nil, adapterErr("news search result is not serializable")
	}
	return out, nil
}

// readTopics decodes the TopicExtraction object: the topic queries and news headlines (blank ones dropped).
func readTopics(v any) (topics, news []string, err error) {
	b, _ := json.Marshal(v)
	var in struct {
		Topics *[]struct{ Query string }    `json:"topics"`
		News   *[]struct{ Headline string } `json:"news"`
	}
	if json.Unmarshal(b, &in) != nil || in.Topics == nil || in.News == nil {
		return nil, nil, fmt.Errorf("the topic_extraction result is not a TopicExtraction object")
	}
	for _, t := range *in.Topics {
		if s := strings.TrimSpace(t.Query); s != "" {
			topics = append(topics, s)
		}
	}
	for _, n := range *in.News {
		if s := strings.TrimSpace(n.Headline); s != "" {
			news = append(news, s)
		}
	}
	return topics, news, nil
}

// readSourceLanguage is the lower-cased source_language of the bound translation result, "" when unknown.
func readSourceLanguage(v any) string {
	b, _ := json.Marshal(v)
	var tr struct {
		SourceLanguage any `json:"source_language"`
	}
	_ = json.Unmarshal(b, &tr)
	s, _ := tr.SourceLanguage.(string)
	if s = strings.ToLower(strings.TrimSpace(s)); langCodeRe.MatchString(s) {
		return s
	}
	return ""
}

// resolveLanguages replaces `source` by the source language (dropped when unknown) and removes duplicates.
func resolveLanguages(configured []string, source string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range configured {
		if l == "source" {
			l = source
		}
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// buildQueries lists the entries: topics, then news, each in every language (Q is set once known).
func buildQueries(cfg registry.NewsSearch, topics, news, languages []string) []*searchQuery {
	var out []*searchQuery
	add := func(origin, original string) {
		for _, l := range languages {
			out = append(out, &searchQuery{Original: original, Origin: origin, Language: l, Endpoint: "news",
				Status: "succeeded", idx: len(out)})
		}
	}
	for _, t := range topics[:min(len(topics), cfg.MaxTopics)] {
		add("topic", t)
	}
	for _, n := range news[:min(len(news), cfg.MaxNews)] {
		add("news", n)
	}
	return out
}
