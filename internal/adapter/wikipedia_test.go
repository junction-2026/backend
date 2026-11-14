package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// The fixtures in testdata/wikipedia have the shape of MediaWiki action=query answers (formatversion=2).
const wikiContact = "mailto:ops@example.org"

type wikiReqRec struct {
	lang   string
	params url.Values
	ua     string
}

// wikiStub stands in for MediaWiki; a search is answered from the fixture routes[lang|srsearch] (default "empty"),
// and the following page request from the same fixture's _pages file.
type wikiStub struct {
	*httptest.Server
	mu       sync.Mutex
	routes   map[string]string
	pagesFor map[string]string
	reqs     []wikiReqRec
	fail     func(rec wikiReqRec, n int) int // non-zero: answer with this status
	delay    time.Duration
	inFlight int
	maxIn    int
}

func newWikiStub(t *testing.T, routes map[string]string) *wikiStub {
	s := &wikiStub{routes: routes, pagesFor: map[string]string{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := wikiReqRec{lang: strings.Split(r.URL.Path, "/")[1], params: r.URL.Query(), ua: r.Header.Get("User-Agent")}
		s.mu.Lock()
		s.reqs = append(s.reqs, rec)
		n := len(s.reqs)
		s.inFlight++
		s.maxIn = max(s.maxIn, s.inFlight)
		s.mu.Unlock()
		time.Sleep(s.delay)
		defer func() { s.mu.Lock(); s.inFlight--; s.mu.Unlock() }()
		if s.fail != nil {
			if code := s.fail(rec, n); code != 0 {
				w.WriteHeader(code)
				return
			}
		}
		var file string
		if rec.params.Get("list") == "search" {
			fx := s.routes[rec.lang+"|"+rec.params.Get("srsearch")]
			if fx == "" {
				fx = "empty"
			}
			file = fx + "_search.json"
			b, err := os.ReadFile(filepath.Join("testdata", "wikipedia", file))
			require.NoError(t, err)
			var sr struct {
				Query struct{ Search []struct{ Title string } }
			}
			require.NoError(t, json.Unmarshal(b, &sr))
			var titles []string
			for _, h := range sr.Query.Search {
				titles = append(titles, h.Title)
			}
			s.mu.Lock()
			s.pagesFor[rec.lang+"|"+strings.Join(titles, "|")] = fx + "_pages.json"
			s.mu.Unlock()
		} else {
			s.mu.Lock()
			file = s.pagesFor[rec.lang+"|"+rec.params.Get("titles")]
			s.mu.Unlock()
		}
		b, err := os.ReadFile(filepath.Join("testdata", "wikipedia", file))
		if err != nil {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *wikiStub) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.reqs) }

// searches lists "lang|srsearch" of the search requests in arrival order.
func (s *wikiStub) searches() (out []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.reqs {
		if r.params.Get("list") == "search" {
			out = append(out, r.lang+"|"+r.params.Get("srsearch"))
		}
	}
	return out
}

func wikiDef(base, dir string, mod func(*registry.Wikipedia)) *registry.Analyzer {
	w := registry.Wikipedia{MinCheckWorthiness: 0.4, MaxClaims: 3, MaxHits: 5, MaxPassageChars: 1500, MaxConcurrency: 2,
		CacheDir: dir, CacheTTL: time.Hour, UserAgent: wikiContact, BaseURLTemplate: base + "/{lang}/w/api.php"}
	if mod != nil {
		mod(&w)
	}
	return &registry.Analyzer{ID: "wikipedia", Execution: registry.Execution{Adapter: registry.AdapterWikipedia, Timeout: 20 * time.Second, Wiki: w}}
}

func wikiRequest(source string, claims ...claim) Request {
	ext, _ := json.Marshal(map[string]any{"topics": []any{}, "news": []any{}, "claims": claims})
	in := map[string]any{"extraction": json.RawMessage(ext)}
	if source != "" {
		in["translation"] = json.RawMessage(fmt.Sprintf(`{"translated_text":"x","source_language":%q}`, source))
	}
	return Request{JobID: "j1", AnalyzerID: "wikipedia", Inputs: in}
}

func wc(text, q, qen string, worth float64, entities ...string) claim {
	return claim{Text: text, Language: "de", Query: q, QueryEN: qen, Entities: entities, CheckWorthiness: worth}
}

type wikiOut struct {
	Languages    []string
	Claims       []claimLookup
	RequestsUsed int `json:"requests_used"`
	Cached       int
}

func runWiki(t *testing.T, x *Executor, d *registry.Analyzer, req Request) wikiOut {
	t.Helper()
	raw, err := x.Execute(context.Background(), d, req)
	require.NoError(t, err)
	var o wikiOut
	require.NoError(t, json.Unmarshal(raw, &o))
	return o
}

func wikiRunErr(t *testing.T, x *Executor, d *registry.Analyzer, req Request) *job.RunError {
	t.Helper()
	raw, err := x.Execute(context.Background(), d, req)
	require.Nil(t, raw)
	re, ok := err.(*job.RunError)
	require.True(t, ok, "%T %v", err, err)
	return re
}

func fastWiki(t *testing.T) {
	old := wikiBackoff
	wikiBackoff = time.Millisecond
	t.Cleanup(func() { wikiBackoff = old })
}

var (
	cduClaim  = wc("Angela Merkel was a member of the CDU.", "Merkel CDU", "Merkel CDU", 0.8, "Angela Merkel", "CDU")
	pensClaim = wc("The government raised the pension age to 70.", "Rentenalter 70", "retirement age 70", 0.9, "government", "pension age", "70")
)

func TestWikiQualificationAndCap(t *testing.T) {
	s := newWikiStub(t, nil)
	var cs []claim
	for i, w := range []float64{0.9, 0.8, 0.3, 0.6, 0.5} {
		cs = append(cs, wc(fmt.Sprintf("claim %d", i), "q", fmt.Sprintf("en%d", i), w, "Entity"))
	}
	o := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", cs...))
	require.Equal(t, []string{"claim 0", "claim 1", "claim 3"}, []string{o.Claims[0].Claim, o.Claims[1].Claim, o.Claims[2].Claim})
	require.Len(t, o.Claims, 3)
	require.Equal(t, []string{"en|en0", "en|en1", "en|en3"}, sortedStrings(s.searches()))
	require.Equal(t, 0.9, o.Claims[0].CheckWorthiness)
}

func sortedStrings(in []string) []string {
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

func TestWikiSkipsWithoutQualifyingClaim(t *testing.T) {
	s := newWikiStub(t, nil)
	for _, req := range []Request{wikiRequest("de", wc("x", "q", "q", 0.2)), wikiRequest("de"),
		{Inputs: map[string]any{"extraction": map[string]any{"topics": []any{}}}}} {
		_, err := (&Executor{}).Execute(context.Background(), wikiDef(s.URL, t.TempDir(), nil), req)
		sk, ok := err.(*Skip)
		require.True(t, ok, "%v", err)
		require.Equal(t, "no_checkable_claims", sk.Reason)
	}
	require.Equal(t, 0, s.count())
}

func TestWikiExtractionNotAnObject(t *testing.T) {
	re := wikiRunErr(t, &Executor{}, wikiDef("http://x", t.TempDir(), nil), Request{Inputs: map[string]any{"extraction": "nope"}})
	require.Equal(t, job.CodeAdapterError, re.Code)
}

func TestWikiGermanAndEnglish(t *testing.T) {
	s := newWikiStub(t, map[string]string{"de|Angela Merkel": "merkel_de", "en|Angela Merkel": "merkel_en"})
	c := wc("Angela Merkel war Kanzlerin.", "Angela Merkel", "Angela Merkel", 0.8, "Angela Merkel")
	o := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("DE", c))
	require.Equal(t, []string{"de", "en"}, o.Languages)
	require.Len(t, o.Claims, 1)
	require.Equal(t, "title", o.Claims[0].SearchMode)
	require.Len(t, o.Claims[0].Wikis, 2)
	de, en := o.Claims[0].Wikis[0], o.Claims[0].Wikis[1]
	require.Equal(t, "de", de.Language)
	require.Equal(t, "en", en.Language)
	require.Equal(t, "found", de.Status)
	require.Equal(t, "Angela Merkel (* 17. Juli 1954 in Hamburg) ist eine deutsche Politikerin (CDU).", *de.Passage)
	require.Equal(t, "lead", *de.PassageSource)
	require.Equal(t, "2026-06-01T10:30:00Z", *de.RevisedAt) // +02:00 converted to UTC
	require.Equal(t, "https://de.wikipedia.org/w/index.php?title=Angela_Merkel&oldid=7000040", *de.URL)
	require.EqualValues(t, 40, *de.PageID)
	require.EqualValues(t, 7000040, *de.RevisionID)
	require.Equal(t, "https://en.wikipedia.org/w/index.php?title=Angela_Merkel&oldid=7000041", *en.URL)
	require.Equal(t, []string{"Angela Merkel"}, de.MatchedEntities)
	require.Equal(t, []string{}, de.MatchedNumbers)
	require.Equal(t, 4, o.RequestsUsed) // search + pages per wiki
	require.Equal(t, 0, o.Cached)
	// the query of the post's wiki is Claim.query, the English one query_en
	require.Equal(t, []string{"de|Angela Merkel", "en|Angela Merkel"}, sortedStrings(s.searches()))
}

func TestWikiQueryPerWiki(t *testing.T) {
	s := newWikiStub(t, nil)
	runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("de", pensClaim))
	require.Equal(t, []string{"de|Rentenalter 70", "en|retirement age 70"}, sortedStrings(s.searches()))
}

func TestWikiEnglishOnly(t *testing.T) {
	for _, source := range []string{"en", "EN", "", "deu", "d", "zz1", "de-AT"} {
		s := newWikiStub(t, nil)
		o := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest(source, pensClaim, cduClaim))
		require.Equal(t, []string{"en"}, o.Languages, source)
		require.Equal(t, []string{"en|Merkel CDU", "en|retirement age 70"}, sortedStrings(s.searches()), source)
		for _, c := range o.Claims {
			require.Len(t, c.Wikis, 1)
			require.Equal(t, "en", c.Wikis[0].Language)
		}
	}
}

func TestWikiSearchRequestShape(t *testing.T) {
	s := newWikiStub(t, map[string]string{"en|retirement age 70": "retirement"})
	runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), func(w *registry.Wikipedia) { w.MaxHits = 4 }), wikiRequest("en", pensClaim))
	require.Len(t, s.reqs, 2)
	q := s.reqs[0].params
	require.Equal(t, "query", q.Get("action"))
	require.Equal(t, "4", q.Get("srlimit"))
	require.Equal(t, "snippet", q.Get("srprop"))
	p := s.reqs[1].params
	require.Equal(t, "Retirement age|Pension", p.Get("titles"))
	require.Equal(t, "1", p.Get("redirects"))
	require.Equal(t, "pageprops|revisions|extracts", p.Get("prop"))
	require.Equal(t, "disambiguation", p.Get("ppprop"))
	require.Contains(t, p.Get("rvprop"), "ids")
	require.Contains(t, p.Get("rvprop"), "timestamp")
	require.Equal(t, "1", p.Get("exintro"))
	require.Equal(t, "1", p.Get("explaintext"))
}

func TestWikiSearchModes(t *testing.T) {
	require.Equal(t, "fulltext", searchMode(pensClaim))
	require.Equal(t, "title", searchMode(cduClaim))
	require.Equal(t, "fulltext", searchMode(wc("The summit took place.", "q", "q", 1, "2024", "-", " ")))
	require.Equal(t, "fulltext", searchMode(wc("The summit took place.", "q", "q", 1)))
	require.Equal(t, "title", searchMode(wc("The summit.", "q", "q", 1, "2024", "Gipfel")))
}

func TestWikiTitleModePicksEntityMatchFulltextTopHit(t *testing.T) {
	s := newWikiStub(t, map[string]string{"en|cdu": "cdu"})
	title := wc("The CDU is a party.", "cdu", "cdu", 0.7, "CDU")
	o := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", title))
	w := o.Claims[0].Wikis[0]
	require.Equal(t, "CDU", *w.Title)
	require.Equal(t, "The CDU is the German party. Angela Merkel led it.", *w.Passage)
	require.Equal(t, "lead", *w.PassageSource)

	full := wc("The CDU got 30 percent.", "cdu", "cdu", 0.7, "CDU")
	o = runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", full))
	w = o.Claims[0].Wikis[0]
	require.Equal(t, "fulltext", o.Claims[0].SearchMode)
	require.Equal(t, "Christian Democratic Union of Germany", *w.Title)
	require.Equal(t, "snippet", *w.PassageSource)
	require.Equal(t, "The CDU is a party", *w.Passage)
	require.Equal(t, []string{"CDU"}, w.MatchedEntities)
	require.Equal(t, []string{}, w.MatchedNumbers) // 30 is not in the snippet
}

func TestWikiQualifiedTitleIsEntityMatch(t *testing.T) {
	s := newWikiStub(t, map[string]string{"en|mercury": "planet"})
	c := wc("Mercury is a planet.", "mercury", "mercury", 0.7, "Mercury")
	w := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", c)).Claims[0].Wikis[0]
	require.Equal(t, "Mercury (planet)", *w.Title)
	require.Equal(t, "https://en.wikipedia.org/w/index.php?title=Mercury_%28planet%29&oldid=6000022", *w.URL)
}

func TestWikiAmbiguousAndDisambiguatedByOtherEntity(t *testing.T) {
	s := newWikiStub(t, map[string]string{"en|mercury": "merc_only", "en|freddie": "mercury"})
	amb := wc("Mercury is hot.", "mercury", "mercury", 0.7, "Mercury")
	w := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", amb)).Claims[0].Wikis[0]
	require.Equal(t, wikiLookup{Language: "en", Query: "mercury", Status: "ambiguous", Coverage: CoverageNone}, w)
	b, _ := json.Marshal(w)
	require.JSONEq(t, `{"language":"en","query":"mercury","status":"ambiguous","cached":false,"title":null,"url":null,"page_id":null,
"revision_id":null,"revised_at":null,"passage":null,"passage_source":null,"matched_entities":null,"matched_numbers":null,"coverage":"no_coverage","evidence_sentence":null}`, string(b))

	ok := wc("Freddie Mercury sang.", "freddie", "freddie", 0.7, "Mercury", "Freddie Mercury")
	w = runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", ok)).Claims[0].Wikis[0]
	require.Equal(t, "found", w.Status)
	require.Equal(t, "Freddie Mercury", *w.Title)
	require.Equal(t, []string{"Mercury", "Freddie Mercury"}, w.MatchedEntities)
}

func TestWikiFulltextDisambiguationTopHit(t *testing.T) {
	s := newWikiStub(t, map[string]string{"en|mercury": "mercury"})
	c := wc("Mercury was 5 times hotter.", "mercury", "mercury", 0.7, "Freddie Mercury")
	w := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", c)).Claims[0].Wikis[0]
	require.Equal(t, "Freddie Mercury", *w.Title) // the top hit is a disambiguation page; the entity match wins
}

func TestWikiNoPage(t *testing.T) {
	s := newWikiStub(t, nil)
	w := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("de", pensClaim)).Claims[0].Wikis
	require.Equal(t, wikiLookup{Language: "de", Query: "Rentenalter 70", Status: "no_page", Coverage: CoverageNone}, w[0])
	require.Equal(t, wikiLookup{Language: "en", Query: "retirement age 70", Status: "no_page", Coverage: CoverageNone}, w[1])
	require.Equal(t, 2, s.count()) // no page request without hits
}

func TestWikiSnippetMarkupAndNumbers(t *testing.T) {
	s := newWikiStub(t, map[string]string{"en|retirement age 70": "retirement"})
	w := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", pensClaim)).Claims[0].Wikis[0]
	require.Equal(t, "Retirement age", *w.Title)
	require.Equal(t, "https://en.wikipedia.org/w/index.php?title=Retirement_age&oldid=1250000000", *w.URL)
	require.EqualValues(t, 1234, *w.PageID)
	require.Equal(t, "2026-10-02T08:15:00Z", *w.RevisedAt)
	require.Equal(t, "the retirement age to 70 & more", *w.Passage)
	require.Equal(t, "snippet", *w.PassageSource)
	require.Equal(t, []string{"70"}, w.MatchedNumbers)
	require.Equal(t, []string{"70"}, w.MatchedEntities) // "government" and "pension age" are not in the snippet
}

func TestMatchedValues(t *testing.T) {
	claimText := "The government raised the pension age to 70 in 2025."
	p := "In many countries the pension age is 67; some plan 70."
	require.Equal(t, []string{"pension age", "70"}, matchedEntities([]string{"government", "pension age", "70", "pension age", " "}, p))
	require.Equal(t, []string{"70"}, matchedNumbers(claimText, p))
	require.Equal(t, []string{}, matchedNumbers("age 70", "born in 1970 and 703"))
	require.Equal(t, []string{"2025", "70"}, matchedNumbers("in 2025 and 70 and 2025", "70 then 2025"))
	require.Equal(t, []string{"STRASSE"}, matchedEntities([]string{"STRASSE"}, "Hauptstraße ist strasse"))
	require.Equal(t, []string{"Ärger"}, matchedEntities([]string{"Ärger"}, "ÄRGER"))
}

func TestWikiRedirectResolved(t *testing.T) {
	s := newWikiStub(t, map[string]string{"en|eu": "redirect"})
	c := wc("The EU is a union.", "eu", "eu", 0.7, "EU")
	w := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", c)).Claims[0].Wikis[0]
	require.Equal(t, "European Union", *w.Title)
	require.EqualValues(t, 8000051, *w.RevisionID)
}

func TestWikiLeadCutOnRuneBoundary(t *testing.T) {
	require.Equal(t, "ÄÖÜ", cutRunes("ÄÖÜäöü", 3))
	require.Equal(t, "abc", cutRunes("abc", 10))
	s := newWikiStub(t, map[string]string{"en|merkel cdu": "merkel_de"})
	c := wc("Merkel", "x", "merkel cdu", 0.7, "Angela Merkel")
	w := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), func(w *registry.Wikipedia) { w.MaxPassageChars = 14 }), wikiRequest("en", c)).Claims[0].Wikis[0]
	require.Equal(t, "Angela Merkel ", *w.Passage)
}

func TestWikiCacheSurvivesNewExecutor(t *testing.T) {
	s := newWikiStub(t, map[string]string{"de|Angela Merkel": "merkel_de", "en|Angela Merkel": "merkel_en"})
	dir := t.TempDir()
	c := wc("Angela Merkel war Kanzlerin.", "Angela Merkel", "Angela Merkel", 0.8, "Angela Merkel")
	first := runWiki(t, &Executor{}, wikiDef(s.URL, dir, nil), wikiRequest("de", c, pensClaim))
	require.Equal(t, 6, first.RequestsUsed) // two searches and two page requests, plus the two empty searches
	n := s.count()
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	require.Len(t, files, 4)

	second := runWiki(t, &Executor{}, wikiDef(s.URL, dir, nil), wikiRequest("de", c, pensClaim))
	require.Equal(t, n, s.count())
	require.Equal(t, 0, second.RequestsUsed)
	require.Equal(t, 4, second.Cached)
	for i, cl := range second.Claims {
		for j, w := range cl.Wikis {
			require.True(t, w.Cached)
			w.Cached = false
			require.Equal(t, first.Claims[i].Wikis[j], w)
		}
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	require.Empty(t, left)
}

func TestWikiCacheKeyAndMisses(t *testing.T) {
	require.Equal(t, wikiKey("en", "Retirement  Age 70", "title"), wikiKey("en", "  retirement age 70 ", "title"))
	require.Equal(t, wikiKey("en", "é", "title"), wikiKey("en", "é", "title")) // NFC
	require.NotEqual(t, wikiKey("en", "x", "title"), wikiKey("en", "x", "fulltext"))
	require.NotEqual(t, wikiKey("en", "x", "title"), wikiKey("de", "x", "title"))

	s := newWikiStub(t, map[string]string{"en|Merkel CDU": "merkel_en"})
	dir := t.TempDir()
	run := func(ttl time.Duration) wikiOut {
		return runWiki(t, &Executor{}, wikiDef(s.URL, dir, func(w *registry.Wikipedia) { w.CacheTTL = ttl }), wikiRequest("en", cduClaim))
	}
	require.Equal(t, 2, run(time.Hour).RequestsUsed)
	file := filepath.Join(dir, wikiKey("en", "Merkel CDU", "title")+".json")
	require.FileExists(t, file)
	require.Equal(t, 0, run(time.Hour).RequestsUsed)
	require.Equal(t, 2, run(time.Nanosecond).RequestsUsed) // expired entries are ignored (and rewritten)
	require.NoError(t, os.WriteFile(file, []byte("{not json"), 0o600))
	require.Equal(t, 2, run(time.Hour).RequestsUsed) // a corrupt file is a miss
	require.Equal(t, 0, run(time.Hour).RequestsUsed)
}

func TestWikiNoPageAndAmbiguousAreCachedFailuresAreNot(t *testing.T) {
	fastWiki(t)
	s := newWikiStub(t, map[string]string{"en|mercury": "merc_only"})
	dir := t.TempDir()
	amb := wc("Mercury is hot.", "mercury", "mercury", 0.7, "Mercury")
	miss := wc("Nothing here.", "q", "none", 0.7, "Nothing")
	o := runWiki(t, &Executor{}, wikiDef(s.URL, dir, nil), wikiRequest("en", amb, miss))
	o = runWiki(t, &Executor{}, wikiDef(s.URL, dir, nil), wikiRequest("en", amb, miss))
	require.Equal(t, 2, o.Cached)
	require.Equal(t, 0, o.RequestsUsed)
	require.Equal(t, "ambiguous", o.Claims[0].Wikis[0].Status)

	s.fail = func(wikiReqRec, int) int { return 500 }
	dir2 := t.TempDir()
	wikiRunErr(t, &Executor{}, wikiDef(s.URL, dir2, nil), wikiRequest("en", amb))
	files, _ := filepath.Glob(filepath.Join(dir2, "*"))
	require.Empty(t, files)
}

func TestWikiInRunDedup(t *testing.T) {
	s := newWikiStub(t, map[string]string{"en|Merkel CDU": "merkel_en", "en|  merkel   CDU ": "merkel_en"})
	a := wc("Angela Merkel was a member of the CDU.", "x", "Merkel CDU", 0.8, "Angela Merkel")
	b := wc("Merkel led the CDU.", "x", "  merkel   CDU ", 0.7, "CDU", "Merkel")
	o := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", a, b))
	require.Equal(t, 2, s.count())
	require.Equal(t, 2, o.RequestsUsed)
	require.Equal(t, 1, o.Cached)
	require.False(t, o.Claims[0].Wikis[0].Cached)
	w := o.Claims[1].Wikis[0]
	require.True(t, w.Cached)
	require.Equal(t, "  merkel   CDU ", w.Query) // the query as sent for this claim
	require.Equal(t, []string{"Merkel"}, w.MatchedEntities)
	require.Equal(t, []string{"Angela Merkel"}, o.Claims[0].Wikis[0].MatchedEntities)
}

func TestWikiUnavailableTwice503(t *testing.T) {
	fastWiki(t)
	s := newWikiStub(t, map[string]string{"de|Angela Merkel": "merkel_de"})
	s.fail = func(r wikiReqRec, _ int) int {
		if r.lang == "en" {
			return 503
		}
		return 0
	}
	c := wc("Angela Merkel war Kanzlerin.", "Angela Merkel", "Angela Merkel", 0.8, "Angela Merkel")
	re := wikiRunErr(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("de", c))
	require.Equal(t, job.CodeAdapterError, re.Code)
	require.Contains(t, re.Message, "en.wikipedia.org")
	require.Contains(t, re.Message, "503")
	require.NotContains(t, re.Message, "127.0.0.1")
	require.NotContains(t, re.Message, "Hamburg") // no passage of the de page
}

func TestWikiRetryOnceThenSucceeds(t *testing.T) {
	fastWiki(t)
	s := newWikiStub(t, map[string]string{"en|Merkel CDU": "merkel_en"})
	s.fail = func(_ wikiReqRec, n int) int {
		if n == 1 {
			return 429
		}
		return 0
	}
	o := runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", cduClaim))
	require.Equal(t, 3, o.RequestsUsed) // the retry is counted
	require.Equal(t, "found", o.Claims[0].Wikis[0].Status)
}

func TestWikiOtherStatusAndUndecodable(t *testing.T) {
	fastWiki(t)
	s := newWikiStub(t, nil)
	s.fail = func(wikiReqRec, int) int { return 403 }
	re := wikiRunErr(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", cduClaim))
	require.Contains(t, re.Message, "HTTP 403")
	require.Equal(t, 1, s.count()) // no retry of a 4xx

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"error":{"code":"x"}}`)) }))
	defer bad.Close()
	re = wikiRunErr(t, &Executor{}, wikiDef(bad.URL, t.TempDir(), nil), wikiRequest("en", cduClaim))
	require.Equal(t, job.CodeAdapterError, re.Code)
	require.Contains(t, re.Message, "could not be decoded")

	gone := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	gone.Close()
	re = wikiRunErr(t, &Executor{}, wikiDef(gone.URL, t.TempDir(), nil), wikiRequest("en", cduClaim))
	require.Equal(t, job.CodeAdapterError, re.Code)
	require.NotContains(t, re.Message, "127.0.0.1")
}

func TestWikiUserAgentOnEveryRequest(t *testing.T) {
	s := newWikiStub(t, map[string]string{"en|Merkel CDU": "merkel_en"})
	runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("de", cduClaim))
	require.Len(t, s.reqs, 3) // de: no hits; en: search and pages
	for _, r := range s.reqs {
		require.Equal(t, "JunctionOrchestrator/1.0 ("+wikiContact+")", r.ua)
	}
}

func TestWikiConcurrencyLimit(t *testing.T) {
	s := newWikiStub(t, nil)
	s.delay = 20 * time.Millisecond
	cs := []claim{wc("a", "a", "a", 1, "A"), wc("b", "b", "b", 1, "B"), wc("c", "c", "c", 1, "C")}
	runWiki(t, &Executor{}, wikiDef(s.URL, t.TempDir(), func(w *registry.Wikipedia) { w.MaxConcurrency = 2 }), wikiRequest("de", cs...))
	require.Equal(t, 6, s.count())
	require.LessOrEqual(t, s.maxIn, 2)
	require.Equal(t, 2, s.maxIn)
}

func TestWikiTimeoutAndCancel(t *testing.T) {
	s := newWikiStub(t, nil)
	s.delay = 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := (&Executor{}).Execute(ctx, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", cduClaim))
	require.Equal(t, job.CodeTimeout, err.(*job.RunError).Code)

	ctx, cancel2 := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel2)
	_, err = (&Executor{}).Execute(ctx, wikiDef(s.URL, t.TempDir(), nil), wikiRequest("en", cduClaim))
	require.Equal(t, job.CodeCancelled, err.(*job.RunError).Code)
}

func TestWikiFirstFailureCancelsRemaining(t *testing.T) {
	s := newWikiStub(t, nil)
	s.delay = 50 * time.Millisecond
	s.fail = func(_ wikiReqRec, n int) int {
		if n == 1 {
			return 404
		}
		return 0
	}
	cs := []claim{wc("a", "a", "a", 1, "A"), wc("b", "b", "b", 1, "B"), wc("c", "c", "c", 1, "C")}
	re := wikiRunErr(t, &Executor{}, wikiDef(s.URL, t.TempDir(), func(w *registry.Wikipedia) { w.MaxConcurrency = 1 }), wikiRequest("en", cs...))
	require.Contains(t, re.Message, "HTTP 404")
	require.Less(t, s.count(), 6)
}
