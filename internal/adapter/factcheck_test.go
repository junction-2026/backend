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
		RetryOf               *string                         `json:"retry_of"`
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
	// "tax rose 40 percent" returns 0 claims and gets its 3-word retry; the other searches have no shorter query
	require.ElementsMatch(t, []string{"de:Rentenalter auf 70", "en:pension age 70", "de:Steuer 40 Prozent", "en:tax rose 40 percent", "en:tax rose 40"}, s.pairs())
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
	require.Equal(t, []string{"de:Rentenalter auf 70", "en:pension age 70", "de:Steuer 40 Prozent", "en:tax rose 40 percent", "en:tax rose 40"}, order, "claims order, query before query_en, retry after its original")
	require.Equal(t, 5, res.Requests)
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
			var sk *Skip
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
	ms := time.Millisecond
	d.Execution.CacheTTL = &ms
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

func TestFactCacheHardCap(t *testing.T) {
	var c factCache
	for i := 0; i < maxCacheEntries+200; i++ {
		c.put(fmt.Sprint("k", i), []providerClaim{{}}, time.Hour)
	}
	require.LessOrEqual(t, len(c.m), maxCacheEntries)
	got, ok := c.get(fmt.Sprint("k", maxCacheEntries+199))
	require.True(t, ok, "the newest entry is cached")
	require.Len(t, got, 1)

	// expired entries are dropped first
	var d factCache
	for i := 0; i < maxCacheEntries; i++ {
		d.put(fmt.Sprint("old", i), nil, time.Nanosecond)
	}
	d.put("fresh", nil, time.Hour)
	time.Sleep(time.Millisecond)
	d.put("fresh2", nil, time.Hour)
	require.Equal(t, 2, len(d.m))
}

func TestFactCheckOversizedAnswerNotCached(t *testing.T) {
	big := `{"claims":[{"text":"` + strings.Repeat("x", maxCachedAnswer) + `"}]}`
	s := newFCStub(t, func(fcReq, int) (int, string) { return 200, big })
	x := &Executor{}
	for i := 0; i < 2; i++ {
		_, err := runFC(context.Background(), x, fcDef(s.URL), extraction(cl{"A", "en", "q", "q", 0.9}))
		require.NoError(t, err)
	}
	require.Equal(t, 2, s.count())
}

// ent is a claim with entities, for the shorter-retry tests.
func extractionEnt(c cl, entities ...string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"claims": []map[string]any{{"text": c.Text, "language": c.Lang, "query": c.Q,
		"query_en": c.QEN, "entities": entities, "check_worthiness": c.W}}})
	return b
}

func TestShorterRetryQueryBuilding(t *testing.T) {
	for _, tc := range []struct {
		name         string
		q, qen       string
		entities     []string
		wantQ, wantE string // "" = no retry
	}{
		{"entities and three words", "EU beschließt Bargeld abschaffen 2027", "EU decides to abolish cash 2027", []string{"EU", "Bargeld", "2027"}, "EU Bargeld", "EU decides to"},
		{"blank entities skipped and trimmed", "a b c", "x y", []string{"  ", " Bargeld ", "", "EU "}, "Bargeld EU", ""},
		{"fewer than two entities", "long query here", "abolish cash", []string{" ", "Bargeld"}, "Bargeld", ""},
		{"no entities", "long query here", "abolish cash", []string{}, "", ""},
		{"equal to original, case and whitespace", "eu  BARGELD", "Abolish   Cash now", []string{"EU", "Bargeld"}, "", ""},
		{"three words of a whitespace-heavy query_en", "q", " one\ttwo\n three four ", nil, "", "one two three"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := planFactChecks(extractionEntRaw(tc.q, tc.qen, tc.entities), 0.4, 3)
			require.Len(t, plan, 2)
			require.Equal(t, tc.wantQ, plan[0].short)
			require.Equal(t, tc.wantE, plan[1].short)
		})
	}
	// an English claim has only the claim-language search: entities rule
	plan := planFactChecks(extractionEntRaw("pension age raised to 70", "pension age raised to 70", []string{"pension", "age"}), 0.4, 3)
	require.Len(t, plan, 2)
	plan = planFactChecks(json.RawMessage(`[{"text":"t","language":"en","query":"pension age raised","query_en":"x","entities":["pension","age"],"check_worthiness":0.9}]`), 0.4, 3)
	require.Len(t, plan, 1)
	require.Equal(t, "pension age", plan[0].short)
}

func extractionEntRaw(q, qen string, entities []string) json.RawMessage {
	b, _ := json.Marshal([]map[string]any{{"text": "t", "language": "de", "query": q, "query_en": qen, "entities": entities, "check_worthiness": 0.9}})
	return b
}

var bargeld = cl{"Die EU hat beschlossen, das Bargeld 2027 abzuschaffen.", "de", "EU beschließt Bargeld abschaffen 2027", "EU decides to abolish cash 2027", 0.9}

func TestShorterRetryFindsResults(t *testing.T) {
	s := newFCStub(t, func(r fcReq, _ int) (int, string) {
		if r.query == "EU Bargeld" {
			return 200, review("EU schafft Bargeld ab", "https://dpa.example/bargeld", "2026-11-01T00:00:00Z", "Falsch")
		}
		return 200, `{}`
	})
	got, err := runFC(context.Background(), &Executor{}, fcDef(s.URL), extractionEnt(bargeld, "EU", "Bargeld", "2027"))
	require.NoError(t, err)
	// planned de, retry of de (entities), planned en, retry of en (3 words, nothing found)
	require.Equal(t, []string{"de:EU beschließt Bargeld abschaffen 2027", "de:EU Bargeld", "en:EU decides to abolish cash 2027", "en:EU decides to"}, sortedByOrder(s.pairs(), []string{
		"de:EU beschließt Bargeld abschaffen 2027", "de:EU Bargeld", "en:EU decides to abolish cash 2027", "en:EU decides to"}))
	res := decodeFC(t, got)
	require.Equal(t, 4, res.Requests)
	require.Len(t, res.Queries, 4)
	for i, want := range []struct {
		q, lang string
		claims  int
		retryOf *string
	}{
		{"EU beschließt Bargeld abschaffen 2027", "de", 0, nil},
		{"EU Bargeld", "de", 1, ptr("EU beschließt Bargeld abschaffen 2027")},
		{"EU decides to abolish cash 2027", "en", 0, nil},
		{"EU decides to", "en", 0, ptr("EU decides to abolish cash 2027")},
	} {
		q := res.Queries[i]
		require.Equal(t, want.q, q.QQ)
		require.Equal(t, want.lang, q.Lang)
		require.Equal(t, want.claims, q.Claims)
		require.Equal(t, bargeld.Text, q.For, "retry keeps for_claim")
		require.Equal(t, "succeeded", q.Status)
		require.Equal(t, want.retryOf, q.RetryOf)
	}
	require.Contains(t, string(got), `"retry_of":null`, "planned searches carry retry_of: null")
	require.Len(t, res.FactChecks, 1)
	require.Equal(t, "EU Bargeld", res.FactChecks[0]["query"])
	require.Equal(t, bargeld.Text, res.FactChecks[0]["for_claim"])
}

func ptr(s string) *string { return &s }

func sortedByOrder(in, order []string) []string {
	var out []string
	for _, o := range order {
		for _, i := range in {
			if i == o {
				out = append(out, i)
			}
		}
	}
	return out
}

func TestShorterRetryNotForFailuresOrRetries(t *testing.T) {
	for name, answer := range map[string]func(fcReq, int) (int, string){
		"503 twice":    func(fcReq, int) (int, string) { return 503, `` },
		"429 twice":    func(fcReq, int) (int, string) { return 429, `{}` },
		"decode error": func(fcReq, int) (int, string) { return 200, `not json` },
	} {
		s := newFCStub(t, answer)
		_, err := runFC(context.Background(), &Executor{}, fcDef(s.URL), extractionEnt(bargeld, "EU", "Bargeld"))
		require.Error(t, err, name) // every search failed
		for _, p := range s.pairs() {
			require.Contains(t, []string{"de:EU beschließt Bargeld abschaffen 2027", "en:EU decides to abolish cash 2027"}, p, name)
		}
	}

	// a retry that finds nothing is not retried; the original's backoff retry is unaffected
	s := newFCStub(t, empty)
	got, err := runFC(context.Background(), &Executor{}, fcDef(s.URL), extractionEnt(bargeld, "EU", "Bargeld"))
	require.NoError(t, err)
	require.Equal(t, 4, s.count(), "two planned searches, one shorter retry each")
	require.Equal(t, 4, decodeFC(t, got).Requests)

	// a failing retry is listed failed, the analyzer still succeeds and the retry is not retried again; 503 once gets the backoff retry
	s = newFCStub(t, func(r fcReq, n int) (int, string) {
		if r.query == "EU Bargeld" {
			return 500, ``
		}
		return 200, `{}`
	})
	got, err = runFC(context.Background(), &Executor{}, fcDef(s.URL), extractionEnt(bargeld, "EU", "Bargeld"))
	require.NoError(t, err)
	res := decodeFC(t, got)
	require.Equal(t, "failed", res.Queries[1].Status)
	require.Equal(t, "search_failed", res.Queries[1].Error.Code)
	require.Equal(t, 2+2+1, res.Requests, "de, de retry x2 (backoff), en, en retry")
}

func TestShorterRetryBoundCacheAndOrder(t *testing.T) {
	three := []cl{
		{"A", "de", "a1 a2 a3 a4", "en a1 a2 a3 a4", 0.9}, {"B", "fr", "b1 b2 b3 b4", "en b1 b2 b3 b4", 0.8}, {"C", "es", "c1 c2 c3 c4", "en c1 c2 c3 c4", 0.7}}
	ext := func() json.RawMessage {
		var claims []map[string]any
		for _, c := range three {
			claims = append(claims, map[string]any{"text": c.Text, "language": c.Lang, "query": c.Q, "query_en": c.QEN,
				"entities": []string{"e1", "e2", "e3"}, "check_worthiness": c.W})
		}
		b, _ := json.Marshal(map[string]any{"claims": claims})
		return b
	}()
	s := newFCStub(t, empty)
	x := &Executor{}
	got, err := runFC(context.Background(), x, fcDef(s.URL), ext)
	require.NoError(t, err)
	res := decodeFC(t, got)
	require.Len(t, res.Queries, 12)
	require.Equal(t, 12, res.Requests)
	require.LessOrEqual(t, s.count(), 12)
	var order []string
	for i, q := range res.Queries {
		order = append(order, q.Lang+":"+q.QQ)
		require.Equal(t, i%2 == 1, q.RetryOf != nil, "retries directly follow their original")
	}
	require.Equal(t, []string{"de:a1 a2 a3 a4", "de:e1 e2", "en:en a1 a2 a3 a4", "en:en a1 a2", "fr:b1 b2 b3 b4", "fr:e1 e2",
		"en:en b1 b2 b3 b4", "en:en b1 b2", "es:c1 c2 c3 c4", "es:e1 e2", "en:en c1 c2 c3 c4", "en:en c1 c2"}, order)

	// a repeated job within the cache lifetime: retries are cached like any search
	before := s.count()
	got, err = runFC(context.Background(), x, fcDef(s.URL), ext)
	require.NoError(t, err)
	res = decodeFC(t, got)
	require.Equal(t, before, s.count())
	require.Zero(t, res.Requests)
	require.Len(t, res.Queries, 12)
	require.Equal(t, 12, res.Cached)
	for _, q := range res.Queries {
		require.True(t, q.Cached)
	}
}

func TestShorterRetryRejectedKeyStopsFurtherRequests(t *testing.T) {
	s := newFCStub(t, func(r fcReq, _ int) (int, string) {
		if r.query == "EU Bargeld" {
			return 400, `{"error":{"message":"API key not valid"}}`
		}
		return 200, `{}`
	})
	d := fcDef(s.URL)
	one := 1
	d.Execution.MaxConcurrency = &one
	got, err := runFC(context.Background(), &Executor{}, d, extractionEnt(bargeld, "EU", "Bargeld"))
	require.Nil(t, got)
	var re *job.RunError
	require.ErrorAs(t, err, &re)
	require.Contains(t, re.Message, "api_key")
	pairs := s.pairs()
	require.Equal(t, "de:EU Bargeld", pairs[len(pairs)-1], "no request after the rejected retry")
	require.LessOrEqual(t, len(pairs), 4)
}
