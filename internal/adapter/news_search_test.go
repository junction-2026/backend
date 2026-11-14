package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

const (
	serperKey  = "serper-SECRET-key"
	newsMisKey = "mistral-SECRET-key"
)

type serperReqRec struct {
	path string
	key  string
	body map[string]any
}

// serperStub stands in for Serper: reply decides the answer for the nth (1-based) request.
type serperStub struct {
	*httptest.Server
	mu       sync.Mutex
	reqs     []serperReqRec
	inFlight int
	maxIn    int
	delay    time.Duration
	reply    func(rec serperReqRec, n int) (int, string)
}

func newSerper(t *testing.T, reply func(rec serperReqRec, n int) (int, string)) *serperStub {
	s := &serperStub{reply: reply}
	if reply == nil {
		s.reply = func(rec serperReqRec, _ int) (int, string) { return 200, serperAnswer(rec, 3) }
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec := serperReqRec{path: r.URL.Path, key: r.Header.Get("X-API-KEY")}
		_ = json.Unmarshal(b, &rec.body)
		s.mu.Lock()
		s.reqs = append(s.reqs, rec)
		n := len(s.reqs)
		s.inFlight++
		s.maxIn = max(s.maxIn, s.inFlight)
		s.mu.Unlock()
		time.Sleep(s.delay)
		s.mu.Lock()
		s.inFlight--
		s.mu.Unlock()
		code, body := s.reply(rec, n)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *serperStub) count(path string) (n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.reqs {
		if path == "" || r.path == path {
			n++
		}
	}
	return n
}

// serperAnswer returns n results for the request, in the shape of its endpoint; every URL is unique.
func serperAnswer(rec serperReqRec, n int) string {
	key, field := "news", "news"
	if rec.path == "/search" {
		field = "organic"
	}
	var items []map[string]any
	for i := 1; i <= n; i++ {
		items = append(items, map[string]any{"title": fmt.Sprintf("%s %d", rec.body["q"], i), "snippet": "s",
			"link": fmt.Sprintf("https://site%d.example/%s/%s/%s", i, rec.path[1:], rec.body["hl"], strings.ReplaceAll(fmt.Sprint(rec.body["q"]), " ", "-")),
			"date": "3 hours ago", "source": fmt.Sprintf("Outlet %d", i)})
	}
	b, _ := json.Marshal(map[string]any{field: items, "searchParameters": map[string]any{"type": key}})
	return string(b)
}

// mistralStub translates the JSON array it gets by prefixing every string with "EN:" (or the reply override).
type mistralStub struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
	hdrs   []http.Header
	reply  func(n int, in []string) (int, string)
}

func newMistralStub(t *testing.T, reply func(n int, in []string) (int, string)) *mistralStub {
	m := &mistralStub{reply: reply}
	if reply == nil {
		m.reply = func(_ int, in []string) (int, string) {
			out := make([]string, len(in))
			for i, s := range in {
				out[i] = "EN:" + s
			}
			b, _ := json.Marshal(out)
			return 200, chat(string(b), nil)
		}
	}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		msgs, _ := body["messages"].([]any)
		var in []string
		if len(msgs) == 2 {
			_ = json.Unmarshal([]byte(msgs[1].(map[string]any)["content"].(string)), &in)
		}
		m.mu.Lock()
		m.bodies, m.hdrs = append(m.bodies, body), append(m.hdrs, r.Header)
		n := len(m.bodies)
		m.mu.Unlock()
		code, out := m.reply(n, in)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *mistralStub) calls() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.bodies) }

func newsDef(serper, mistral string, mod func(*registry.NewsSearch)) *registry.Analyzer {
	n := registry.NewsSearch{Languages: []string{"source", "en"},
		Translate: registry.NewsTranslate{APIKey: newsMisKey, Model: "mistral-small-latest", Endpoint: mistral},
		MaxTopics: 3, MaxNews: 2, Num: 8, MinResults: 2, PerDomain: 2, MaxResults: 12, MaxConcurrency: 4, CacheTTL: time.Hour}
	if mod != nil {
		mod(&n)
	}
	return &registry.Analyzer{ID: "news_search", Execution: registry.Execution{Adapter: registry.AdapterNewsSearch,
		BaseURL: serper, APIKey: serperKey, Timeout: 20 * time.Second, News: n}}
}

// newsReq builds the request: topic queries, news headlines and the translation's source language ("" = null).
func newsReq(topics, news []string, source string) Request {
	ts := []map[string]any{}
	for _, q := range topics {
		ts = append(ts, map[string]any{"title": q, "query": q, "description": nil})
	}
	ns := []map[string]any{}
	for _, h := range news {
		ns = append(ns, map[string]any{"headline": h, "summary": nil})
	}
	top, _ := json.Marshal(map[string]any{"topics": ts, "news": ns, "input_truncated": false, "usage": nil})
	in := map[string]any{"topics": json.RawMessage(top)}
	if source != "" {
		in["translation"] = json.RawMessage(fmt.Sprintf(`{"translated_text":"x","source_language":%q}`, source))
	}
	return Request{JobID: "j1", AnalyzerID: "news_search", Inputs: in}
}

type newsOut struct {
	Languages    []string
	Queries      []searchQuery
	Articles     []article
	SearchesUsed int `json:"searches_used"`
	Cached       int
}

func runNews(t *testing.T, x *Executor, d *registry.Analyzer, req Request) newsOut {
	t.Helper()
	raw, err := x.Execute(context.Background(), d, req)
	require.NoError(t, err)
	var o newsOut
	require.NoError(t, json.Unmarshal(raw, &o))
	return o
}

func newsRunErr(t *testing.T, x *Executor, d *registry.Analyzer, req Request) *job.RunError {
	t.Helper()
	_, err := x.Execute(context.Background(), d, req)
	re, ok := err.(*job.RunError)
	require.True(t, ok, "%T %v", err, err)
	return re
}

func fast(t *testing.T) {
	old := newsRetryBackoff
	newsRetryBackoff = time.Millisecond
	t.Cleanup(func() { newsRetryBackoff = old })
}

func TestNewsTwoTopicsGermanAndEnglish(t *testing.T) {
	s, m := newSerper(t, nil), newMistralStub(t, nil)
	o := runNews(t, &Executor{}, newsDef(s.URL, m.URL, nil), newsReq([]string{"Spritpreis Erhöhung", "Wahl Ergebnis"}, nil, "DE"))
	require.Equal(t, []string{"de", "en"}, o.Languages)
	require.Len(t, o.Queries, 4)
	require.Equal(t, 4, o.SearchesUsed)
	require.Equal(t, 0, o.Cached)
	require.Equal(t, 4, s.count("/news"))
	require.Equal(t, 0, s.count("/search"))
	want := [][3]string{{"Spritpreis Erhöhung", "de", "Spritpreis Erhöhung"}, {"Spritpreis Erhöhung", "en", "EN:Spritpreis Erhöhung"},
		{"Wahl Ergebnis", "de", "Wahl Ergebnis"}, {"Wahl Ergebnis", "en", "EN:Wahl Ergebnis"}}
	for i, q := range o.Queries {
		require.Equal(t, want[i][0], q.Original)
		require.Equal(t, want[i][1], q.Language)
		require.Equal(t, want[i][2], q.Q)
		require.Equal(t, "topic", q.Origin)
		require.Equal(t, "news", q.Endpoint)
		require.Equal(t, "succeeded", q.Status)
		require.Equal(t, 3, q.Results)
		require.Nil(t, q.Error)
	}
	// one translation request for both English queries, one request per language
	require.Equal(t, 1, m.calls())
	require.Equal(t, "Bearer "+newsMisKey, m.hdrs[0].Get("Authorization"))
	require.Equal(t, "mistral-small-latest", m.bodies[0]["model"])
	// Serper request shape: key header, hl/gl table, num
	for _, r := range s.reqs {
		require.Equal(t, serperKey, r.key)
		require.EqualValues(t, 8, r.body["num"])
		require.Equal(t, map[string]string{"de": "de", "en": "us"}[r.body["hl"].(string)], r.body["gl"])
	}
}

func TestNewsOrderTopicsThenNewsAndLimits(t *testing.T) {
	s, m := newSerper(t, nil), newMistralStub(t, nil)
	d := newsDef(s.URL, m.URL, func(n *registry.NewsSearch) { n.MaxTopics, n.MaxNews = 2, 1 })
	o := runNews(t, &Executor{}, d, newsReq([]string{"t1", "t2", "t3"}, []string{"h1", "h2"}, "de"))
	var got []string
	for _, q := range o.Queries {
		got = append(got, q.Origin+"/"+q.Original+"/"+q.Language)
	}
	require.Equal(t, []string{"topic/t1/de", "topic/t1/en", "topic/t2/de", "topic/t2/en", "news/h1/de", "news/h1/en"}, got)
	require.Equal(t, 1, m.calls())
	require.Len(t, m.bodies, 1)
	require.Contains(t, m.bodies[0]["messages"].([]any)[1].(map[string]any)["content"], `"t1","t2","h1"`)
}

func TestNewsSourceUnknownTranslatesEveryLanguage(t *testing.T) {
	s, m := newSerper(t, nil), newMistralStub(t, nil)
	d := newsDef(s.URL, m.URL, func(n *registry.NewsSearch) { n.Languages = []string{"source", "en", "fr", "en"} })
	for _, src := range []string{"", "german"} { // null / not a code
		o := runNews(t, &Executor{}, d, newsReq([]string{"a"}, nil, src))
		require.Equal(t, []string{"en", "fr"}, o.Languages)
		require.Equal(t, "EN:a", o.Queries[0].Q)
	}
	require.Equal(t, 4, m.calls()) // one per language and run
	// no translation binding at all
	req := newsReq([]string{"a"}, nil, "")
	delete(req.Inputs, "translation")
	require.Equal(t, []string{"en", "fr"}, runNews(t, &Executor{}, d, req).Languages)
}

func TestNewsSourceOnlyAndUnknownFails(t *testing.T) {
	s := newSerper(t, nil)
	d := newsDef(s.URL, "http://127.0.0.1:1", func(n *registry.NewsSearch) { n.Languages = []string{"source"}; n.Translate = registry.NewsTranslate{} })
	o := runNews(t, &Executor{}, d, newsReq([]string{"a"}, nil, "uk"))
	require.Equal(t, 1, o.SearchesUsed)
	require.Equal(t, 1, s.count("/news"))
	require.Equal(t, "uk", s.reqs[0].body["hl"])
	require.Equal(t, "uk", s.reqs[0].body["gl"])
	re := newsRunErr(t, &Executor{}, d, newsReq([]string{"a"}, nil, ""))
	require.Equal(t, job.CodeAdapterError, re.Code)
}

func TestNewsFallbackOnlyForShortQuery(t *testing.T) {
	s := newSerper(t, func(rec serperReqRec, _ int) (int, string) {
		if rec.path == "/news" && rec.body["q"] == "b" {
			return 200, serperAnswer(rec, 1)
		}
		return 200, serperAnswer(rec, 3)
	})
	m := newMistralStub(t, nil)
	d := newsDef(s.URL, m.URL, func(n *registry.NewsSearch) { n.Languages = []string{"source"} })
	o := runNews(t, &Executor{}, d, newsReq([]string{"a", "b"}, nil, "de"))
	require.Equal(t, 3, o.SearchesUsed)
	require.Equal(t, 1, s.count("/search"))
	require.Equal(t, "b", s.reqs[len(s.reqs)-1].body["q"])
	require.Equal(t, "news", o.Queries[0].Endpoint)
	require.Equal(t, "search", o.Queries[1].Endpoint)
	require.Equal(t, 1+3, o.Queries[1].Results)
	// the web results join the articles of that entry
	n := 0
	for _, a := range o.Articles {
		if strings.Contains(a.URL, "/search/") {
			n++
		}
	}
	require.Positive(t, n)
}

func TestNewsFallbackFailureKeepsNewsResults(t *testing.T) {
	fast(t)
	s := newSerper(t, func(rec serperReqRec, _ int) (int, string) {
		if rec.path == "/search" {
			return 500, "boom"
		}
		return 200, serperAnswer(rec, 1)
	})
	d := newsDef(s.URL, "http://127.0.0.1:1", func(n *registry.NewsSearch) { n.Languages = []string{"source"} })
	o := runNews(t, &Executor{}, d, newsReq([]string{"a"}, nil, "de"))
	require.Equal(t, "succeeded", o.Queries[0].Status)
	require.Equal(t, "news", o.Queries[0].Endpoint)
	require.Equal(t, 1, o.Queries[0].Results)
	require.Equal(t, 3, o.SearchesUsed) // news + two web attempts
	require.Len(t, o.Articles, 1)
}

func TestNewsTranslationFailureMarksEntries(t *testing.T) {
	fast(t)
	for name, reply := range map[string]func(int, []string) (int, string){
		"http error":  func(int, []string) (int, string) { return 500, "down" },
		"not array":   func(int, []string) (int, string) { return 200, chat(`{"a":1}`, nil) },
		"wrong count": func(int, []string) (int, string) { return 200, chat(`["only one"]`, nil) },
		"empty item":  func(int, []string) (int, string) { return 200, chat(`["x",""]`, nil) },
		"no choices":  func(int, []string) (int, string) { return 200, `{"choices":[]}` },
	} {
		t.Run(name, func(t *testing.T) {
			s, m := newSerper(t, nil), newMistralStub(t, reply)
			o := runNews(t, &Executor{}, newsDef(s.URL, m.URL, nil), newsReq([]string{"a", "b"}, nil, "de"))
			require.Equal(t, "succeeded", o.Queries[0].Status)
			for _, i := range []int{1, 3} {
				q := o.Queries[i]
				require.Equal(t, "en", q.Language)
				require.Equal(t, "failed", q.Status)
				require.Equal(t, "translation_failed", q.Error.Code)
				require.Zero(t, q.Results)
			}
			require.Equal(t, 2, s.count("/news")) // no search for the failed entries
			for _, r := range s.reqs {
				require.Equal(t, "de", r.body["hl"])
			}
			require.Equal(t, 2, o.SearchesUsed)
		})
	}
}

func TestNewsTranslationFencedReplyAccepted(t *testing.T) {
	s := newSerper(t, nil)
	m := newMistralStub(t, func(int, []string) (int, string) { return 200, chat("```json\n[\"x\",\"y\"]\n```", nil) })
	o := runNews(t, &Executor{}, newsDef(s.URL, m.URL, nil), newsReq([]string{"a", "b"}, nil, "de"))
	require.Equal(t, "x", o.Queries[1].Q)
	require.Equal(t, "y", o.Queries[3].Q)
}

func TestNewsNothingToSearch(t *testing.T) {
	s, m := newSerper(t, nil), newMistralStub(t, nil)
	_, err := (&Executor{}).Execute(context.Background(), newsDef(s.URL, m.URL, nil), newsReq(nil, nil, "de"))
	var sk *Skip
	require.ErrorAs(t, err, &sk)
	require.Equal(t, "no_searchable_topics", sk.Reason)
	require.Zero(t, s.count(""))
	require.Zero(t, m.calls())
}

func TestNewsBadTopicsInput(t *testing.T) {
	d := newsDef("http://127.0.0.1:1", "http://127.0.0.1:1", nil)
	for _, in := range []any{nil, json.RawMessage(`null`), json.RawMessage(`{"topics":[]}`), json.RawMessage(`[1]`)} {
		re := newsRunErr(t, &Executor{}, d, Request{Inputs: map[string]any{"topics": in}})
		require.Equal(t, job.CodeAdapterError, re.Code)
		require.Contains(t, re.Message, "TopicExtraction")
	}
}

func TestNewsMergeDedupLimitsAndOrder(t *testing.T) {
	s := newSerper(t, func(rec serperReqRec, _ int) (int, string) {
		type it = map[string]any
		var items []it
		if rec.body["hl"] == "de" {
			items = []it{
				{"title": "A", "link": "https://www.Example.com/a#top", "date": "Nov 12, 2026"},
				{"title": "B", "link": "https://example.com/b", "date": "1 day ago"},
				{"title": "C", "link": "ftp://example.com/c"},
				{"title": "D", "link": "javascript:alert(1)"},
				{"title": "E", "link": "https://example.com/e"},
			}
		} else {
			items = []it{
				{"title": "A again", "link": "https://WWW.EXAMPLE.COM/a"},
				{"title": "F", "link": "https://other.org/f", "snippet": "<img src=x onerror=alert(1)>", "source": "Other"},
				{"title": "", "link": "https://third.net/g"},
			}
		}
		b, _ := json.Marshal(map[string]any{"news": items})
		return 200, string(b)
	})
	m := newMistralStub(t, nil)
	d := newsDef(s.URL, m.URL, func(n *registry.NewsSearch) { n.MinResults = 1; n.MaxResults = 4 })
	o := runNews(t, &Executor{}, d, newsReq([]string{"a"}, nil, "de"))
	var urls []string
	for _, a := range o.Articles {
		urls = append(urls, a.URL)
	}
	// rank 1: de A (dated), en "A again" is a duplicate; rank 2: de B (dated) before en F (undated); rank 3: third.net (en) before e
	require.Equal(t, []string{"https://www.example.com/a", "https://example.com/b", "https://other.org/f", "https://third.net/g"}, urls)
	require.Equal(t, "example.com", o.Articles[0].Domain)
	require.Equal(t, "de", o.Articles[0].Language)
	require.Equal(t, "a", o.Articles[0].Query)
	require.Equal(t, "topic", o.Articles[0].Origin)
	require.Equal(t, "Nov 12, 2026", *o.Articles[0].Date)
	require.Nil(t, o.Articles[0].Source)
	require.Equal(t, "<img src=x onerror=alert(1)>", *o.Articles[2].Snippet)
	require.Equal(t, "Other", *o.Articles[2].Source)
	require.Nil(t, o.Articles[2].Date)
	require.Equal(t, "https://third.net/g", o.Articles[3].Title) // blank title falls back to the URL
}

func TestNewsPerDomainLimit(t *testing.T) {
	s := newSerper(t, func(rec serperReqRec, _ int) (int, string) {
		var items []map[string]any
		for i := 0; i < 5; i++ {
			items = append(items, map[string]any{"title": "t", "link": fmt.Sprintf("https://news.example.com/%d", i)})
		}
		items = append(items, map[string]any{"title": "o", "link": "https://other.org/x"})
		b, _ := json.Marshal(map[string]any{"news": items})
		return 200, string(b)
	})
	d := newsDef(s.URL, "", func(n *registry.NewsSearch) { n.Languages = []string{"source"} })
	o := runNews(t, &Executor{}, d, newsReq([]string{"a"}, nil, "de"))
	require.Len(t, o.Articles, 3)
	require.Equal(t, "news.example.com", o.Articles[0].Domain)
	require.Equal(t, "news.example.com", o.Articles[1].Domain)
	require.Equal(t, "other.org", o.Articles[2].Domain)
}

func TestNewsCacheAcrossJobsAndSharedRequests(t *testing.T) {
	s, m := newSerper(t, nil), newMistralStub(t, nil)
	x := &Executor{}
	d := newsDef(s.URL, m.URL, nil)
	req := newsReq([]string{"a", "b"}, nil, "de")
	first := runNews(t, x, d, req)
	require.Equal(t, 4, first.SearchesUsed)
	second := runNews(t, x, d, req)
	require.Equal(t, 0, second.SearchesUsed)
	require.Equal(t, 4, second.Cached)
	for _, q := range second.Queries {
		require.True(t, q.Cached)
	}
	require.Equal(t, 4, s.count(""))
	require.Equal(t, first.Articles, second.Articles)

	// identical queries within one run share one request
	s2 := newSerper(t, nil)
	s2.delay = 50 * time.Millisecond
	d2 := newsDef(s2.URL, m.URL, func(n *registry.NewsSearch) { n.Languages = []string{"source"} })
	o := runNews(t, &Executor{}, d2, newsReq([]string{"same", "same"}, nil, "de"))
	require.Equal(t, 1, s2.count(""))
	require.Equal(t, 1, o.SearchesUsed)
	require.Equal(t, 1, o.Cached)
}

func TestNewsCacheExpiresAndFailuresAreNotCached(t *testing.T) {
	fast(t)
	s := newSerper(t, func(rec serperReqRec, n int) (int, string) {
		if n <= 2 {
			return 503, "down"
		}
		return 200, serperAnswer(rec, 3)
	})
	x := &Executor{}
	d := newsDef(s.URL, "", func(n *registry.NewsSearch) { n.Languages = []string{"source"}; n.CacheTTL = 50 * time.Millisecond })
	req := newsReq([]string{"a"}, nil, "de")
	re := newsRunErr(t, x, d, req) // 503 twice: every entry failed
	require.Equal(t, job.CodeAdapterError, re.Code)
	require.Equal(t, 1, runNews(t, x, d, req).SearchesUsed) // not cached: sent again
	time.Sleep(80 * time.Millisecond)
	require.Equal(t, 1, runNews(t, x, d, req).SearchesUsed) // expired
}

func TestNewsRetryAndPartialFailure(t *testing.T) {
	fast(t)
	// q "a": 429 then 200 -> succeeded; q "b" in en: 429 twice -> failed
	var mu sync.Mutex
	seen := map[string]int{}
	s := newSerper(t, func(rec serperReqRec, _ int) (int, string) {
		k := fmt.Sprint(rec.body["q"], "|", rec.body["hl"])
		mu.Lock()
		seen[k]++
		n := seen[k]
		mu.Unlock()
		switch {
		case rec.body["q"] == "a" && n == 1:
			return 429, "slow down"
		case rec.body["q"] == "EN:b":
			return 429, "slow down"
		}
		return 200, serperAnswer(rec, 3)
	})
	m := newMistralStub(t, nil)
	o := runNews(t, &Executor{}, newsDef(s.URL, m.URL, nil), newsReq([]string{"a", "b"}, nil, "de"))
	require.Equal(t, "succeeded", o.Queries[0].Status)
	require.Equal(t, "failed", o.Queries[3].Status)
	require.Equal(t, "search_failed", o.Queries[3].Error.Code)
	require.Contains(t, o.Queries[3].Error.Message, "HTTP")
	require.Zero(t, o.Queries[3].Results)
	require.Equal(t, 6, o.SearchesUsed) // a/de twice, a/en, b/de, b/en twice
	require.NotEmpty(t, o.Articles)
	for _, a := range o.Articles {
		require.NotEqual(t, "EN:b", a.Query)
	}
}

func TestNewsAllSearchesFail(t *testing.T) {
	fast(t)
	s := newSerper(t, func(serperReqRec, int) (int, string) { return 503, "" })
	re := newsRunErr(t, &Executor{}, newsDef(s.URL, "", func(n *registry.NewsSearch) { n.Languages = []string{"source"} }), newsReq([]string{"a", "b"}, nil, "de"))
	require.Equal(t, job.CodeAdapterError, re.Code)
	require.Equal(t, 4, s.count("")) // two attempts each
}

func TestNewsRejectedKeysAbort(t *testing.T) {
	for _, code := range []int{401, 403} {
		s := newSerper(t, func(serperReqRec, int) (int, string) { return code, "denied" })
		s.delay = 20 * time.Millisecond
		d := newsDef(s.URL, "", func(n *registry.NewsSearch) {
			n.Languages = []string{"source"}
			n.MaxConcurrency = 1
		})
		var topics []string
		for i := 0; i < 3; i++ {
			topics = append(topics, fmt.Sprint("q", i))
		}
		re := newsRunErr(t, &Executor{}, d, newsReq(topics, nil, "de"))
		require.Equal(t, job.CodeAdapterError, re.Code)
		require.Contains(t, re.Message, "execution.api_key")
		require.NotContains(t, re.Message, "translate")
		require.Equal(t, 1, s.count(""), "the remaining searches are not sent, no retry")
	}
	// Mistral rejects its key: no search is needed to fail, none is retried
	s := newSerper(t, nil)
	m := newMistralStub(t, func(int, []string) (int, string) { return 401, "no" })
	re := newsRunErr(t, &Executor{}, newsDef(s.URL, m.URL, nil), newsReq([]string{"a"}, nil, "de"))
	require.Equal(t, job.CodeAdapterError, re.Code)
	require.Contains(t, re.Message, "execution.translate.api_key")
	require.Equal(t, 1, m.calls())
}

func TestNewsKeysNeverLeak(t *testing.T) {
	fast(t)
	// 403 echoing the key: named setting, no value
	s := newSerper(t, func(rec serperReqRec, _ int) (int, string) {
		return 403, `{"message":"bad key ` + rec.key + `","X-API-KEY":"` + rec.key + `"}`
	})
	re := newsRunErr(t, &Executor{}, newsDef(s.URL, "", func(n *registry.NewsSearch) { n.Languages = []string{"source"} }), newsReq([]string{"a"}, nil, "de"))
	require.Contains(t, re.Message, "execution.api_key")
	require.NotContains(t, re.Message, serperKey)

	// 500 echoing both keys and header values: the failed entry's message, the result and the run error are clean
	echo := func(rec serperReqRec, _ int) (int, string) {
		if rec.body["q"] == "boom" {
			return 500, `oops ` + serperKey + ` ` + newsMisKey + ` X-API-KEY: ` + serperKey + ` Authorization: Bearer ` + newsMisKey
		}
		return 200, serperAnswer(rec, 3)
	}
	s2 := newSerper(t, echo)
	d := newsDef(s2.URL, "", func(n *registry.NewsSearch) { n.Languages = []string{"source"} })
	raw, err := (&Executor{}).Execute(context.Background(), d, newsReq([]string{"a", "boom"}, nil, "de"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), serperKey)
	require.NotContains(t, string(raw), newsMisKey)
	require.Contains(t, string(raw), "[redacted]")

	re = newsRunErr(t, &Executor{}, d, newsReq([]string{"boom"}, nil, "de"))
	require.NotContains(t, re.Message, serperKey)
	require.NotContains(t, re.Message, newsMisKey)

	// Mistral echoes its key in a 500 and in a malformed reply
	s3 := newSerper(t, nil)
	m := newMistralStub(t, func(int, []string) (int, string) {
		return 500, "Authorization: Bearer " + newsMisKey + " " + newsMisKey
	})
	raw, err = (&Executor{}).Execute(context.Background(), newsDef(s3.URL, m.URL, nil), newsReq([]string{"a"}, nil, "de"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), newsMisKey)
}

func TestNewsConcurrencyLimit(t *testing.T) {
	s := newSerper(t, nil)
	s.delay = 30 * time.Millisecond
	d := newsDef(s.URL, "", func(n *registry.NewsSearch) { n.Languages = []string{"source"}; n.MaxConcurrency = 2; n.MaxTopics = 6 })
	runNews(t, &Executor{}, d, newsReq([]string{"a", "b", "c", "d", "e", "f"}, nil, "de"))
	require.Equal(t, 2, s.maxIn)
}

func TestNewsContextCancelAndTimeout(t *testing.T) {
	s := newSerper(t, nil)
	s.delay = 300 * time.Millisecond
	d := newsDef(s.URL, "", func(n *registry.NewsSearch) { n.Languages = []string{"source"} })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := (&Executor{}).Execute(ctx, d, newsReq([]string{"a"}, nil, "de"))
	require.Equal(t, job.CodeTimeout, err.(*job.RunError).Code)
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	_, err = (&Executor{}).Execute(ctx2, d, newsReq([]string{"a"}, nil, "de"))
	require.Equal(t, job.CodeCancelled, err.(*job.RunError).Code)
}

func TestNewsRetryBackoffHonoursContext(t *testing.T) {
	old := newsRetryBackoff
	newsRetryBackoff = time.Hour
	defer func() { newsRetryBackoff = old }()
	s := newSerper(t, func(serperReqRec, int) (int, string) { return 500, "" })
	d := newsDef(s.URL, "", func(n *registry.NewsSearch) { n.Languages = []string{"source"} })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (&Executor{}).Execute(ctx, d, newsReq([]string{"a"}, nil, "de"))
	require.Equal(t, job.CodeTimeout, err.(*job.RunError).Code)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestParseNewsDate(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	for s, want := range map[string]time.Time{
		"3 hours ago": now.Add(-3 * time.Hour), "1 Day ago": now.AddDate(0, 0, -1), "2 weeks ago": now.AddDate(0, 0, -14),
		"Nov 12, 2026": time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), "2026-11-01": time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	} {
		got, ok := parseNewsDate(s, now)
		require.True(t, ok, s)
		require.True(t, want.Equal(got), s)
	}
	for _, s := range []string{"", "yesterday-ish", "soon"} {
		_, ok := parseNewsDate(s, now)
		require.False(t, ok, s)
	}
}

func TestRedactor(t *testing.T) {
	r := redactor("sec/ret+1", "")
	for _, in := range []string{"a sec/ret+1 b", `{"k":"sec/ret+1"}`, "sec%2Fret%2B1", "X-API-KEY: abc", "authorization=Bearer abc"} {
		out := r(in)
		require.NotContains(t, out, "ret")
		require.NotContains(t, out, "abc", in)
		require.Contains(t, out, "[redacted]")
	}
}

func TestNewsCacheIsBounded(t *testing.T) {
	var c newsCache
	for i := 0; i < maxCacheEntries+200; i++ {
		_, _, err := c.do(context.Background(), fmt.Sprint("k", i), time.Hour, func() ([]serperResult, error) { return nil, nil })
		require.NoError(t, err)
	}
	require.LessOrEqual(t, len(c.m), maxCacheEntries+1)
	// the newest entry is still served from the cache
	_, shared, _ := c.do(context.Background(), fmt.Sprint("k", maxCacheEntries+199), time.Hour, func() ([]serperResult, error) { return nil, nil })
	require.True(t, shared)
}
