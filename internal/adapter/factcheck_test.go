package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

const fcKey = "AIzaSy-FACTCHECK-SECRET-key"

func init() { factBackoff = time.Millisecond }

type fcReq struct{ query, lang, key, page, age, path string }

// fcStub is the stub Fact Check endpoint; answer decides per (query, languageCode).
type fcStub struct {
	*httptest.Server
	mu       sync.Mutex
	reqs     []fcReq
	inflight atomic.Int32
	maxSeen  atomic.Int32
	answer   func(r fcReq, n int) (int, string) // n: how many requests for this pair so far
}

func newFCStub(t *testing.T, answer func(r fcReq, n int) (int, string)) *fcStub {
	s := &fcStub{answer: answer}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := s.inflight.Add(1)
		defer s.inflight.Add(-1)
		for m := s.maxSeen.Load(); cur > m && !s.maxSeen.CompareAndSwap(m, cur); m = s.maxSeen.Load() {
		}
		time.Sleep(5 * time.Millisecond)
		q := r.URL.Query()
		fr := fcReq{q.Get("query"), q.Get("languageCode"), q.Get("key"), q.Get("pageSize"), q.Get("maxAgeDays"), r.URL.Path}
		s.mu.Lock()
		s.reqs = append(s.reqs, fr)
		n := 0
		for _, x := range s.reqs {
			if x.query == fr.query && x.lang == fr.lang {
				n++
			}
		}
		s.mu.Unlock()
		code, body := s.answer(fr, n)
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *fcStub) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.reqs) }

func (s *fcStub) pairs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.reqs {
		out = append(out, r.lang+":"+r.query)
	}
	return out
}

func fcDef(url string) *registry.Analyzer {
	return &registry.Analyzer{ID: "fact_check", Execution: registry.Execution{Adapter: registry.AdapterFactCheck,
		BaseURL: url + "/v1alpha1/", APIKey: fcKey, Timeout: 5 * time.Second}}
}

type cl struct {
	Text, Lang, Q, QEN string
	W                  float64
}

func extraction(cs ...cl) json.RawMessage {
	claims := []map[string]any{}
	for _, c := range cs {
		claims = append(claims, map[string]any{"text": c.Text, "language": c.Lang, "query": c.Q, "query_en": c.QEN,
			"entities": []string{}, "check_worthiness": c.W})
	}
	b, _ := json.Marshal(map[string]any{"topics": []any{}, "news": []any{}, "claims": claims, "input_truncated": false, "usage": nil})
	return b
}

func runFC(ctx context.Context, x *Executor, d *registry.Analyzer, ext any) (json.RawMessage, error) {
	return x.Execute(ctx, d, Request{JobID: "j", AnalyzerID: "fact_check", Inputs: map[string]any{"extraction": ext}})
}

type fcResult struct {
	Queries []struct {
		Q, Language, ForClaim string                          `json:"-"`
		QQ                    string                          `json:"q"`
		Lang                  string                          `json:"language"`
		For                   string                          `json:"for_claim"`
		Claims                int                             `json:"claims"`
		Status                string                          `json:"status"`
		Cached                bool                            `json:"cached"`
		Error                 *struct{ Code, Message string } `json:"error"`
	} `json:"queries"`
	FactChecks []map[string]any `json:"fact_checks"`
	Requests   int              `json:"requests_used"`
	Cached     int              `json:"cached"`
}

func decodeFC(t *testing.T, raw json.RawMessage) fcResult {
	t.Helper()
	var r fcResult
	require.NoError(t, json.Unmarshal(raw, &r))
	return r
}

func review(text, u, date, rating string) string {
	return fmt.Sprintf(`{"claims":[{"text":%q,"claimant":"Facebook-Beitrag","claimDate":"2025-03-01T00:00:00Z","claimReview":[`+
		`{"publisher":{"name":"Correctiv","site":"correctiv.org"},"url":%q,"title":"T","reviewDate":%q,"textualRating":%q,"languageCode":"de"}]}]}`,
		text, u, date, rating)
}

func empty(fcReq, int) (int, string) { return 200, `{}` }

var de2 = []cl{
	{"Die Regierung hat das Rentenalter auf 70 erhöht.", "de", "Rentenalter auf 70", "pension age 70", 0.9},
	{"Die Steuer stieg um 40 Prozent.", "de", "Steuer 40 Prozent", "tax rose 40 percent", 0.5},
}

func TestFactCheckPlanAndRequestShape(t *testing.T) {
	s := newFCStub(t, empty)
	got, err := runFC(context.Background(), &Executor{}, fcDef(s.URL), extraction(de2...))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"de:Rentenalter auf 70", "en:pension age 70", "de:Steuer 40 Prozent", "en:tax rose 40 percent"}, s.pairs())
	for _, r := range s.reqs {
		require.Equal(t, "/v1alpha1/claims:search", r.path)
		require.Equal(t, fcKey, r.key)
		require.Equal(t, "5", r.page)
		require.Equal(t, "1825", r.age)
	}
	res := decodeFC(t, got)
	var order []string
	for _, q := range res.Queries {
		order = append(order, q.Lang+":"+q.QQ)
		require.Equal(t, "succeeded", q.Status)
		require.Nil(t, q.Error)
	}
	require.Equal(t, []string{"de:Rentenalter auf 70", "en:pension age 70", "de:Steuer 40 Prozent", "en:tax rose 40 percent"}, order, "claims order, query before query_en")
	require.Equal(t, 4, res.Requests)
	require.Equal(t, `[]`, string(mustField(t, got, "fact_checks")))
	require.NotContains(t, string(got), fcKey)
}

func mustField(t *testing.T, raw json.RawMessage, k string) json.RawMessage {
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))
	return m[k]
}

func TestFactCheckEnglishDuplicatesThresholdAndCap(t *testing.T) {
	s := newFCStub(t, empty)
	_, err := runFC(context.Background(), &Executor{}, fcDef(s.URL), extraction(
		cl{"A", "en-GB", "pension age 70", "pension age 70", 0.8}))
	require.NoError(t, err)
	require.Equal(t, []string{"en-GB:pension age 70"}, s.pairs(), "one request, languageCode as the claim's")

	// same (query, languageCode) of an earlier claim is not searched again; an English query_en pair for a non-English claim too
	s = newFCStub(t, empty)
	_, err = runFC(context.Background(), &Executor{}, fcDef(s.URL), extraction(
		cl{"A", "de", "x", "same", 0.9}, cl{"B", "fr", "y", "same", 0.8}, cl{"C", "en", "same", "same", 0.7}))
	require.NoError(t, err)
	require.Equal(t, []string{"de:x", "en:same", "fr:y"}, sortedCopy(s.pairs()), "en:same sent once")

	// threshold 0.4 and max_claims 3 in claims order: 0.7, 0.9 and 0.8 are searched, 0.2 and the fifth are not
	s = newFCStub(t, empty)
	got, err := runFC(context.Background(), &Executor{}, fcDef(s.URL), extraction(
		cl{"c1", "en", "q1", "q1", 0.7}, cl{"c2", "en", "q2", "q2", 0.2}, cl{"c3", "en", "q3", "q3", 0.9},
		cl{"c4", "en", "q4", "q4", 0.8}, cl{"c5", "en", "q5", "q5", 0.6}))
	require.NoError(t, err)
	require.Equal(t, []string{"en:q1", "en:q3", "en:q4"}, sortedCopy(s.pairs()))
	require.Len(t, decodeFC(t, got).Queries, 3, "no Query for a claim below the threshold")
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestFactCheckSkipWithoutRequests(t *testing.T) {
	s := newFCStub(t, empty)
	for name, ext := range map[string]any{
		"empty claims":    extraction(),
		"below threshold": extraction(cl{"A", "en", "q", "q", 0.1}),
		"missing claims":  json.RawMessage(`{"topics":[],"news":[],"input_truncated":false,"usage":null}`),
		"null claims":     json.RawMessage(`{"claims":null}`),
		"bad claims":      json.RawMessage(`{"claims":"x"}`),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := runFC(context.Background(), &Executor{}, fcDef(s.URL), ext)
			require.Nil(t, got)
			var sk *SkipError
			require.ErrorAs(t, err, &sk)
			require.Equal(t, "no_checkable_claims", sk.Reason)
		})
	}
	require.Zero(t, s.count())

	for _, bad := range []any{nil, json.RawMessage(`[1]`), json.RawMessage(`"x"`), json.RawMessage(`null`)} {
		_, err := runFC(context.Background(), &Executor{}, fcDef(s.URL), bad)
		var re *job.RunError
		require.ErrorAs(t, err, &re)
		require.Equal(t, job.CodeAdapterError, re.Code)
	}
	require.Zero(t, s.count())
}

func TestFactCheckAssemblyAndMapping(t *testing.T) {
	const x, y, z = "https://correctiv.org/x", "https://example.org/y", "https://example.org/z"
	s := newFCStub(t, func(r fcReq, _ int) (int, string) {
		if r.lang == "de" {
			return 200, review("Rente mit 70 beschlossen", x, "2025-03-04T00:00:00Z", " FALSCH. ")
		}
		// the English search repeats x, adds an older review, a review without url, one with an unparseable date
		return 200, `{"claims":[{"text":"older","claimReview":[` +
			`{"url":"` + x + `","reviewDate":"2026-01-01T00:00:00Z","textualRating":"True"},` +
			`{"url":"` + y + `","reviewDate":"2024-01-01T00:00:00Z","textualRating":"Fehlender Kontext"},` +
			`{"url":"","reviewDate":"2026-05-01T00:00:00Z"},` +
			`{"url":"` + z + `","reviewDate":"yesterday"}]}]}`
	})
	got, err := runFC(context.Background(), &Executor{}, fcDef(s.URL), extraction(de2[0]))
	require.NoError(t, err)
	res := decodeFC(t, got)
	require.Len(t, res.FactChecks, 3)
	first := res.FactChecks[0]
	require.Equal(t, x, first["url"])
	require.Equal(t, "Rentenalter auf 70", first["query"], "first in planning order kept: the German query")
	require.Equal(t, de2[0].Text, first["for_claim"])
	require.Equal(t, "Rente mit 70 beschlossen", first["claim"])
	require.Equal(t, "Facebook-Beitrag", first["claimant"])
	require.Equal(t, "2025-03-01T00:00:00Z", first["claim_date"])
	require.Equal(t, "Correctiv", first["publisher"])
	require.Equal(t, "correctiv.org", first["site"])
	require.Equal(t, "T", first["title"])
	require.Equal(t, " FALSCH. ", first["rating"], "verbatim")
	require.Equal(t, "false", first["rating_normalized"])
	require.Equal(t, "de", first["language"])
	require.Equal(t, "2025-03-04T00:00:00Z", first["review_date"])
	require.Equal(t, y, res.FactChecks[1]["url"])
	require.Equal(t, "unrated", res.FactChecks[1]["rating_normalized"])
	require.Equal(t, z, res.FactChecks[2]["url"], "unparseable review_date last")
	require.Nil(t, res.FactChecks[2]["rating"])
	require.Equal(t, 2, res.Requests)
}

func TestFactCheckCapNewestFirst(t *testing.T) {
	// 14 distinct reviews (7 per search), newest are the February ones
	s2 := newFCStub(t, func(r fcReq, _ int) (int, string) {
		m := "01"
		if r.lang == "en" {
			m = "02"
		}
		var rs []string
		for i := 1; i <= 7; i++ {
			rs = append(rs, fmt.Sprintf(`{"url":"https://e.org/%s%d","reviewDate":"2025-%s-%02dT00:00:00Z"}`, r.lang, i, m, i))
		}
		return 200, `{"claims":[{"claimReview":[` + strings.Join(rs, ",") + `]}]}`
	})
	got, err := runFC(context.Background(), &Executor{}, fcDef(s2.URL), extraction(de2[0]))
	require.NoError(t, err)
	res := decodeFC(t, got)
	require.Len(t, res.FactChecks, 10)
	require.Equal(t, "https://e.org/en7", res.FactChecks[0]["url"])
	require.Equal(t, "https://e.org/de5", res.FactChecks[9]["url"], "the 4 oldest are dropped")
}

func TestRatingTable(t *testing.T) {
	for want, terms := range map[string][]string{
		"false":      {"False", "Falsch", "Faux", "Falso", " FALSCH. ", "false!"},
		"misleading": {"Misleading", "Irreführend", "Trompeur"},
		"mixed":      {"Partly true", "Teilweise richtig", "Mixture"},
		"true":       {"True", "Richtig", "Vrai", "true."},
		"unrated":    {"Fehlender Kontext", "", "Unproven", "?"},
	} {
		for _, term := range terms {
			require.Equal(t, want, normalizeRating(&term), term)
		}
	}
	require.Equal(t, "unrated", normalizeRating(nil))
}

func TestFactCheckCache(t *testing.T) {
	s := newFCStub(t, func(r fcReq, _ int) (int, string) {
		return 200, review("c", "https://e.org/"+r.lang, "2025-03-04T00:00:00Z", "False")
	})
	x := &Executor{}
	first, err := runFC(context.Background(), x, fcDef(s.URL), extraction(de2...))
	require.NoError(t, err)
	require.Equal(t, 4, s.count())
	require.Equal(t, 4, decodeFC(t, first).Requests)

	got, err := runFC(context.Background(), x, fcDef(s.URL), extraction(de2...))
	require.NoError(t, err)
	res := decodeFC(t, got)
	require.Equal(t, 4, s.count(), "no request for the second job")
	require.Zero(t, res.Requests)
	require.Equal(t, 4, res.Cached)
	for _, q := range res.Queries {
		require.True(t, q.Cached)
		require.Equal(t, "succeeded", q.Status)
		require.Equal(t, 1, q.Claims)
	}
	require.NotEmpty(t, res.FactChecks)

	// after the TTL a new request is sent
	d := fcDef(s.URL)
	d.Execution.CacheTTL = time.Millisecond
	y := &Executor{}
	_, err = runFC(context.Background(), y, d, extraction(de2[0]))
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	before := s.count()
	_, err = runFC(context.Background(), y, d, extraction(de2[0]))
	require.NoError(t, err)
	require.Equal(t, before+2, s.count())
}

func TestFactCheckFailuresAreNotCached(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	s := newFCStub(t, func(r fcReq, _ int) (int, string) {
		if fail.Load() && r.lang == "en" {
			return 500, `oops`
		}
		return 200, review("c", "https://e.org/"+r.lang, "2025-03-04T00:00:00Z", "False")
	})
	x := &Executor{}
	_, err := runFC(context.Background(), x, fcDef(s.URL), extraction(de2[0]))
	require.NoError(t, err)
	require.Equal(t, 3, s.count(), "de once, en twice (retry)")
	fail.Store(false)
	got, err := runFC(context.Background(), x, fcDef(s.URL), extraction(de2[0]))
	require.NoError(t, err)
	res := decodeFC(t, got)
	require.Equal(t, 1, res.Requests, "only the failed pair is requested again")
	require.Equal(t, 1, res.Cached)
}

func TestFactCheckRetryAndPartialFailure(t *testing.T) {
	s := newFCStub(t, func(r fcReq, n int) (int, string) {
		switch {
		case r.lang == "en": // 429 twice
			return 429, `{"error":{"message":"quota"}}`
		case r.query == "retry-me" && n == 1:
			return 503, ``
		}
		return 200, review("c", "https://e.org/"+r.query, "2025-03-04T00:00:00Z", "False")
	})
	got, err := runFC(context.Background(), &Executor{}, fcDef(s.URL), extraction(
		cl{"A", "de", "retry-me", "en-a", 0.9}, cl{"B", "de", "plain", "en-b", 0.8}))
	require.NoError(t, err, "partial failure succeeds")
	res := decodeFC(t, got)
	require.Equal(t, 4+3, s.count()) // 2 en x2 attempts, retry-me x2, plain x1 -> 7
	require.Equal(t, 7, res.Requests, "retries counted")
	var failed, ok int
	for _, q := range res.Queries {
		if q.Status == "failed" {
			failed++
			require.Zero(t, q.Claims)
			require.Equal(t, "search_failed", q.Error.Code)
			require.NotContains(t, q.Error.Message, fcKey)
			require.Equal(t, "en", q.Lang)
		} else {
			ok++
			require.Nil(t, q.Error)
			require.Equal(t, 1, q.Claims)
		}
	}
	require.Equal(t, 2, failed)
	require.Equal(t, 2, ok)
	require.Len(t, res.FactChecks, 2, "built from the succeeded queries")

	// undecodable answers fail the query too
	s = newFCStub(t, func(r fcReq, _ int) (int, string) {
		if r.lang == "en" {
			return 200, `not json`
		}
		return 200, `{}`
	})
	got, err = runFC(context.Background(), &Executor{}, fcDef(s.URL), extraction(de2[0]))
	require.NoError(t, err)
	require.Equal(t, "failed", decodeFC(t, got).Queries[1].Status)
}

func TestFactCheckEveryQueryFails(t *testing.T) {
	s := newFCStub(t, func(fcReq, int) (int, string) { return 503, `` })
	got, err := runFC(context.Background(), &Executor{}, fcDef(s.URL), extraction(de2[0]))
	require.Nil(t, got)
	var re *job.RunError
	require.ErrorAs(t, err, &re)
	require.Equal(t, job.CodeAdapterError, re.Code)
	require.Equal(t, 4, s.count(), "two queries, one retry each")
}

func TestFactCheckRejectedKeyStopsAndRedacts(t *testing.T) {
	for _, code := range []int{400, 401, 403} {
		s := newFCStub(t, func(r fcReq, _ int) (int, string) {
			echo := "Request had invalid credentials: GET " + "http://x/v1alpha1/claims:search?query=a&key=" + fcKey + " and escaped key=" + url.QueryEscape(fcKey)
			b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": echo}})
			return code, string(b)
		})
		d := fcDef(s.URL)
		one := 1
		d.Execution.MaxConcurrency = &one // queries queue behind the first, which is rejected
		got, err := runFC(context.Background(), &Executor{}, d, extraction(de2...))
		require.Nil(t, got)
		var re *job.RunError
		require.ErrorAs(t, err, &re)
		require.Equal(t, job.CodeAdapterError, re.Code)
		require.Contains(t, re.Message, "api_key")
		require.Contains(t, re.Message, "not enabled")
		require.NotContains(t, re.Message, fcKey)
		require.NotContains(t, re.Message, url.QueryEscape(fcKey))
		require.Equal(t, 1, s.count(), "no further request after HTTP %d", code)
	}
}

func TestFactCheckKeyNeverInTransportErrors(t *testing.T) {
	s := newFCStub(t, empty)
	d := fcDef(s.URL)
	s.Close() // connection refused: net/http's error text embeds the URL
	x := &Executor{}
	_, err := runFC(context.Background(), x, d, extraction(de2[0]))
	require.Error(t, err)
	require.NotContains(t, err.Error(), fcKey)
}

func TestFactCheckConcurrencyBound(t *testing.T) {
	s := newFCStub(t, empty)
	d := fcDef(s.URL)
	two := 2
	d.Execution.MaxConcurrency = &two
	three := 3
	d.Execution.MaxClaims = &three
	_, err := runFC(context.Background(), &Executor{}, d, extraction(
		cl{"a", "de", "a", "a-en", 0.9}, cl{"b", "de", "b", "b-en", 0.9}, cl{"c", "de", "c", "c-en", 0.9}))
	require.NoError(t, err)
	require.Equal(t, 6, s.count())
	require.LessOrEqual(t, s.maxSeen.Load(), int32(2))
	require.EqualValues(t, 2, s.maxSeen.Load())
}

func TestFactCheckTimeoutAndCancel(t *testing.T) {
	s := newFCStub(t, func(fcReq, int) (int, string) { time.Sleep(300 * time.Millisecond); return 200, `{}` })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := runFC(ctx, &Executor{}, fcDef(s.URL), extraction(de2[0]))
	var re *job.RunError
	require.ErrorAs(t, err, &re)
	require.Equal(t, job.CodeTimeout, re.Code)

	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err = runFC(ctx, &Executor{}, fcDef(s.URL), extraction(de2[0]))
	require.ErrorAs(t, err, &re)
	require.Equal(t, job.CodeCancelled, re.Code)
}
