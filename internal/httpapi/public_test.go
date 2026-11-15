package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
)

// countSvc counts store reads and created jobs.
type countSvc struct {
	*fakeSvc
	gets, creates atomic.Int32
}

func (c *countSvc) Get(id string) (job.Job, error) { c.gets.Add(1); return c.fakeSvc.Get(id) }

type pubHarness struct {
	*harness
	cs        *countSvc
	now       time.Time
	noScraper bool
}

// newPublic serves the handler over a fake engine whose Create stores ordinary jobs job-1, job-2, ...
func newPublic(t *testing.T, mod func(*Config)) *pubHarness {
	t.Helper()
	f := newFake()
	x := &pubHarness{cs: &countSvc{fakeSvc: f}, now: t0}
	f.create = func(r engine.CreateRequest) (job.Job, error) {
		if r.Scrape != nil && x.noScraper {
			return job.Job{}, engine.ErrScrapeUnavailable
		}
		n := x.cs.creates.Add(1)
		j := mkJob(fmt.Sprintf("job-%d", n), 0, job.StatusQueued)
		j.Public, j.Options.Finalize = !r.Private, r.Finalize == nil || *r.Finalize
		if r.Scrape != nil {
			j.Scrape = &job.Scrape{Status: job.RunPending, ScrapeRequest: *r.Scrape}
		} else {
			in := r.Input
			j.Input = &in
		}
		f.mu.Lock()
		f.jobs[j.ID] = j
		f.mu.Unlock()
		return j, nil
	}
	logs := &syncBuf{}
	c := Config{Service: x.cs, Registry: loadReg(t, regYAMLText, map[string]string{"FINALIZER_URL": "http://f.invalid", "FINALIZER_TOKEN": "t"}),
		Logger: slog.New(slog.NewJSONHandler(logs, nil)), Now: func() time.Time { return x.now }}
	if mod != nil {
		mod(&c)
	}
	x.harness = &harness{h: NewHandler(c), svc: f, logs: logs}
	return x
}

func (x *pubHarness) put(j job.Job) {
	x.svc.mu.Lock()
	defer x.svc.mu.Unlock()
	x.svc.jobs[j.ID] = j
}

func (x *pubHarness) stored(id string) job.Job {
	x.svc.mu.Lock()
	defer x.svc.mu.Unlock()
	return x.svc.jobs[id]
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), rec.Body.String())
	return m
}

// submit posts body and returns the created job's stored content.
func (x *pubHarness) submit(t *testing.T, body string) job.Job {
	t.Helper()
	rec := x.do("POST", "/api/v1/public/analyses", body)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	return x.stored(decode(t, rec)["id"].(string))
}

var publicMembers = []string{"id", "status", "done", "created_at", "finished_at", "coverage", "input", "finalization_status",
	"result", "techniques", "bot_signal", "evidence", "error", "share_url"}

func TestPublicNoCredentialsNeeded(t *testing.T) {
	x := newPublic(t, nil)
	require.Equal(t, 201, x.do("POST", "/api/v1/public/analyses", `{"post": "Some post text"}`).Code)
	req := httptest.NewRequest("POST", "/api/v1/public/analyses", strings.NewReader(`{"post":"x"}`))
	req.Header.Set("Authorization", "Bearer garbage")
	req.Header.Set("Cookie", "session=nope")
	rec := httptest.NewRecorder()
	x.h.ServeHTTP(rec, req)
	require.Equal(t, 201, rec.Code, "credential headers are ignored")
}

func TestPublicMethodNotAllowed(t *testing.T) {
	x := newPublic(t, nil)
	wantError(t, x.do("DELETE", "/api/v1/public/analyses/job-1", ""), 405, "method_not_allowed")
	wantError(t, x.do("GET", "/api/v1/public/analyses", ""), 405, "method_not_allowed")
	wantError(t, x.do("PUT", "/api/v1/public/analyses", "{}"), 405, "method_not_allowed")
}

func TestPublicSubmitCreatesOneJob(t *testing.T) {
	x := newPublic(t, nil)
	rec := x.do("POST", "/api/v1/public/analyses", `{"post": "P", "comments": ["A", "B", "C"], "api_key": "abc"}`)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	require.Equal(t, int32(1), x.cs.creates.Load())
	require.Equal(t, "/api/v1/public/analyses/job-1", rec.Header().Get("Location"))
	p := decode(t, rec)
	require.Equal(t, "job-1", p["id"])
	require.Equal(t, "queued", p["status"])
	require.Equal(t, false, p["done"])
	for _, k := range publicMembers {
		require.Contains(t, p, k)
	}
	r := x.svc.last
	require.Nil(t, r.Analyzers, "every analyzer selected")
	require.True(t, *r.Finalize)
	require.False(t, r.Private)
	require.Nil(t, r.Scrape)
	require.Equal(t, "text/plain", r.Input.ContentType)
}

func TestPublicComposition(t *testing.T) {
	x := newPublic(t, nil)
	for _, c := range []struct {
		name, body, content string
		hasPost             bool
		count               int
	}{
		{"post only is verbatim", `{"post": "  Vote no!\n"}`, "  Vote no!\n", true, 0},
		{"post with blank comments is verbatim", `{"post": " P ", "comments": ["", "  \t\n"]}`, " P ", true, 0},
		{"post and comments", `{"text": " Vote no! ", "comments": ["  Agreed", "", "Liar!"]}`, "Post:\nVote no!\n\nComment 1:\nAgreed\n\nComment 2:\nLiar!", true, 2},
		{"comments only", `{"comments": "Agreed"}`, "Comment 1:\nAgreed", false, 1},
		{"comment string as array", `{"comments": "Nice try, liar"}`, "Comment 1:\nNice try, liar", false, 1},
		{"blank post with comments", `{"post": "  ", "comments": [" a ", " ", "b"]}`, "Comment 1:\na\n\nComment 2:\nb", false, 2},
		{"null members absent", `{"post": null, "text": "T", "url": null, "comments": null, "public": null}`, "T", true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := x.submit(t, c.body)
			require.Equal(t, c.content, j.Input.Content)
			require.Equal(t, "text/plain", j.Input.ContentType)
			require.JSONEq(t, fmt.Sprintf(`{"public_api":{"has_post":%t,"comment_count":%d}}`, c.hasPost, c.count), string(j.Input.Source.Metadata))
			require.True(t, j.Public)
		})
	}
	a := x.submit(t, `{"comments": "Nice try, liar"}`)
	b := x.submit(t, `{"comments": ["Nice try, liar"]}`)
	require.Equal(t, a.Input, b.Input, "a string counts as a one-element array")

	j := x.submit(t, `{"text": "Vote no!", "comments": ["Agreed", "", "Liar!"]}`)
	require.JSONEq(t, `{"public_api":{"has_post":true,"comment_count":2}}`, string(j.Input.Source.Metadata))
}

func TestPublicHundredComments(t *testing.T) {
	x := newPublic(t, nil)
	cs := make([]string, 100)
	for i := range cs {
		cs[i] = strconv.Itoa(i)
	}
	b, _ := json.Marshal(map[string]any{"comments": cs})
	j := x.submit(t, string(b))
	require.True(t, strings.HasSuffix(j.Input.Content, "Comment 100:\n99"))

	b, _ = json.Marshal(map[string]any{"comments": append(cs, "x")})
	e := wantError(t, x.do("POST", "/api/v1/public/analyses", string(b)), 422, "validation_failed")
	require.Contains(t, e.Error.Details["fields"], "comments")
}

func TestPublicMemberViolations(t *testing.T) {
	x := newPublic(t, nil)
	for _, c := range []struct{ name, body, field string }{
		{"post wrong type", `{"post": 5}`, "post"},
		{"text wrong type", `{"text": []}`, "text"},
		{"url wrong type", `{"url": 5}`, "url"},
		{"comments wrong type", `{"comments": 5}`, "comments"},
		{"comments object", `{"comments": {"a": "b"}}`, "comments"},
		{"wrong element type", `{"comments": ["ok", 5]}`, "comments[1]"},
		{"null element", `{"comments": ["ok", null]}`, "comments[1]"},
		{"post and text", `{"post": "a", "text": "b"}`, "text"},
		{"post and blank text", `{"post": "a", "text": ""}`, "text"},
		{"url with post", `{"post": "a", "url": "https://example.com/x"}`, "url"},
		{"url with comment", `{"comments": ["a"], "url": "https://example.com/x"}`, "url"},
		{"url ftp", `{"url": "ftp://example.com/x"}`, "url"},
		{"url relative", `{"url": "/x"}`, "url"},
		{"url too long", `{"url": "https://example.com/` + strings.Repeat("a", 2048) + `"}`, "url"},
		{"public wrong type", `{"post": "a", "public": "yes"}`, "public"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := wantError(t, x.do("POST", "/api/v1/public/analyses", c.body), 422, "validation_failed")
			require.Contains(t, e.Error.Details["fields"], c.field)
		})
	}
	require.Zero(t, x.cs.creates.Load())
}

func TestPublicValidationOrder(t *testing.T) {
	x := newPublic(t, func(c *Config) { c.PublicSubmitPerMinute = 1000 })
	for _, c := range []struct {
		name, body string
		status     int
		code       string
	}{
		{"empty body", ``, 400, "invalid_request"},
		{"white space body", " \n\t", 400, "invalid_request"},
		{"invalid json", `{"post":`, 400, "invalid_request"},
		{"trailing data", `{"post":"a"} {}`, 400, "invalid_request"},
		{"array", `[1]`, 400, "invalid_request"},
		{"string", `"post"`, 400, "invalid_request"},
		{"null", `null`, 400, "invalid_request"},
		{"empty object", `{}`, 400, "missing_content"},
		{"blank post", `{"post": "   "}`, 400, "missing_content"},
		{"no comments", `{"comments": []}`, 400, "missing_content"},
		{"blank comments only", `{"comments": ["", " "]}`, 400, "missing_content"},
		{"blank url-less with public", `{"public": false}`, 400, "missing_content"},
		{"type error before missing content", `{"post": 5}`, 422, "validation_failed"},
		{"too large", `{"post":"` + strings.Repeat("a", 2<<20) + `"}`, 413, "payload_too_large"},
		{"too large and invalid", strings.Repeat("[", 2<<20), 413, "payload_too_large"},
	} {
		t.Run(c.name, func(t *testing.T) { wantError(t, x.do("POST", "/api/v1/public/analyses", c.body), c.status, c.code) })
	}
	require.Zero(t, x.cs.creates.Load(), "no error creates a job")
}

func TestPublicURLSubmission(t *testing.T) {
	x := newPublic(t, nil)
	const u = "https://bsky.app/profile/alice.bsky.social/post/3kxyz"
	rec := x.do("POST", "/api/v1/public/analyses", `{"url": "`+u+`", "post": "  ", "comments": [""]}`)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	require.Equal(t, &job.ScrapeRequest{URL: u, MaxPosts: 20, MaxComments: 50}, x.svc.last.Scrape)
	require.True(t, *x.svc.last.Finalize)
	require.Equal(t, map[string]any{"content": nil, "url": u}, decode(t, rec)["input"])

	x.noScraper = true
	wantError(t, x.do("POST", "/api/v1/public/analyses", `{"url": "`+u+`"}`), 503, "scrape_unavailable")
	require.Equal(t, int32(1), x.cs.creates.Load())
	// validation and missing content come before scrape_unavailable
	wantError(t, x.do("POST", "/api/v1/public/analyses", `{"url": "ftp://x"}`), 422, "validation_failed")
	wantError(t, x.do("POST", "/api/v1/public/analyses", `{}`), 400, "missing_content")
}

func TestPublicJobsAreOrdinaryJobs(t *testing.T) {
	x := newPublic(t, nil)
	listed := x.submit(t, `{"post": "P"}`)
	hidden := x.submit(t, `{"post": "P", "public": false}`)
	require.False(t, hidden.Public)
	require.True(t, x.svc.last.Private)

	rec := x.do("GET", "/api/v1/jobs/"+listed.ID, "")
	require.Equal(t, 200, rec.Code)
	require.Equal(t, true, decode(t, rec)["options"].(map[string]any)["finalize"])
	require.Equal(t, 200, x.do("GET", "/api/v1/jobs/"+hidden.ID, "").Code)
	list := x.do("GET", "/api/v1/jobs", "").Body.String()
	require.Contains(t, list, `"`+listed.ID+`"`)
	require.NotContains(t, list, `"`+hidden.ID+`"`)
	// a private job is still readable through the public API
	require.Equal(t, 200, x.do("GET", "/api/v1/public/analyses/"+hidden.ID, "").Code)
}

func TestPublicGet(t *testing.T) {
	x := newPublic(t, nil)
	wantError(t, x.do("GET", "/api/v1/public/analyses/does-not-exist", ""), 404, "not_found")

	// a job created through POST /api/v1/jobs
	require.Equal(t, 201, x.do("POST", "/api/v1/jobs", `{"input":{"content":"ui text"}}`).Code)
	rec := x.do("GET", "/api/v1/public/analyses/job-1", "")
	require.Equal(t, 200, rec.Code)
	require.Equal(t, map[string]any{"content": "ui text", "url": nil}, decode(t, rec)["input"])

	running := mkJob("run-1", 0, job.StatusRunning)
	running.Coverage = job.Coverage{Total: 5, Applicable: 4, Pending: 1, Running: 2, Succeeded: 1, Settled: 1}
	running.Analyzers = []job.AnalyzerRun{{ID: "techniques", Status: job.RunRunning}, {ID: "bot_detection", Status: job.RunPending}}
	x.put(running)
	p := decode(t, x.do("GET", "/api/v1/public/analyses/run-1", ""))
	require.Equal(t, false, p["done"])
	require.Equal(t, "running", p["status"])
	require.Nil(t, p["result"])
	require.Equal(t, map[string]any{"total": 5.0, "applicable": 4.0, "pending": 1.0, "running": 2.0, "succeeded": 1.0, "failed": 0.0, "skipped": 0.0, "settled": 1.0}, p["coverage"])
}

func TestPublicFreshJob(t *testing.T) {
	x := newPublic(t, nil)
	rec := x.do("POST", "/api/v1/public/analyses", `{"post": "P"}`)
	require.Equal(t, 201, rec.Code)
	for _, body := range []string{rec.Body.String(), x.do("GET", "/api/v1/public/analyses/job-1", "").Body.String()} {
		var p map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(body), &p))
		for _, k := range publicMembers {
			require.Contains(t, p, k)
		}
		for k, want := range map[string]string{"done": `false`, "finished_at": `null`, "input": `{"content":"P","url":null}`, "result": `null`,
			"techniques": `null`, "bot_signal": `null`, "error": `null`, "share_url": `null`, "finalization_status": `"pending"`,
			"evidence": `{"fact_checks":null,"news":null,"wikipedia":null}`} {
			require.JSONEq(t, want, string(p[k]), k)
		}
	}
}

func run(id, status, result string) job.AnalyzerRun {
	r := job.AnalyzerRun{ID: id, Status: status}
	if result != "" {
		r.Result = json.RawMessage(result)
	}
	return r
}

func (x *pubHarness) view(t *testing.T, j job.Job) map[string]json.RawMessage {
	t.Helper()
	x.put(j)
	rec := x.do("GET", "/api/v1/public/analyses/"+j.ID, "")
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var p map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	return p
}

const (
	tcResult = `{"words": 20, "results": [
	  {"model": "nci-technique", "cutoff": 0.5, "scores": [{"label": "Doubt", "score": 0.71}, {"label": "Loaded_Language", "score": 0.6}]},
	  {"model": "nci-social", "cutoff": 0.3, "scores": [{"label": "Doubt", "score": 0.9}, {"label": "Loaded_Language", "score": 0.8}]}],
	 "detected": [
	  {"name": "Doubt", "display": "Doubt", "models": ["nci-technique"], "strength": "strong", "margin": 0.2,
	   "spans": [{"technique": "Doubt", "text": "Is it true?!", "start": 0, "end": 12, "why": "w"}]},
	  {"name": "Loaded_Language", "display": "Loaded language", "models": ["nci-technique", "nci-social"], "spans": []}],
	 "summary": {}}`
	llmResult = `{"source": "llm", "findings": [{"label": "statistical_cherry_picking", "source": "llm", "name": "Cherry-picked statistic",
	  "definition": "d", "explanation": "e", "confidence": 0.6, "span": {"field": "content", "text": "90%", "start": 10, "end": 13}}]}`
)

func TestPublicTechniques(t *testing.T) {
	x := newPublic(t, nil)
	j := mkJob("t", 0, job.StatusCompleted)
	j.Analyzers = []job.AnalyzerRun{run("techniques", job.RunSucceeded, tcResult), run("llm_techniques", job.RunSucceeded, llmResult)}
	require.JSONEq(t, `[
	  {"source": "classifier", "label": "Doubt", "name": "Doubt", "score": 0.71, "strength": "strong",
	   "spans": [{"field": "content", "start": 0, "end": 12, "text": "Is it true?!"}]},
	  {"source": "classifier", "label": "Loaded_Language", "name": "Loaded language", "score": 0.8, "strength": null, "spans": []},
	  {"source": "llm", "label": "statistical_cherry_picking", "name": "Cherry-picked statistic", "score": 0.6, "strength": null,
	   "spans": [{"field": "content", "start": 10, "end": 13, "text": "90%"}]}]`, string(x.view(t, j)["techniques"]))

	// the spec scenario: classifier only, llm skipped
	j.Analyzers[1] = run("llm_techniques", job.RunSkipped, "")
	var ts []map[string]any
	require.NoError(t, json.Unmarshal(x.view(t, j)["techniques"], &ts))
	require.Len(t, ts, 2)
	require.Equal(t, 0.71, ts[0]["score"], "only the Detection's models count")

	for name, c := range map[string]struct {
		runs []job.AnalyzerRun
		want string
	}{
		"nothing detected": {[]job.AnalyzerRun{run("techniques", job.RunSucceeded, `{"results": [], "detected": []}`),
			run("llm_techniques", job.RunSucceeded, `{"findings": []}`)}, `[]`},
		"no technique analysis": {[]job.AnalyzerRun{run("techniques", job.RunFailed, ""), run("llm_techniques", job.RunSkipped, "")}, `null`},
		"missing runs":          {nil, `null`},
		"running":               {[]job.AnalyzerRun{run("techniques", job.RunRunning, "")}, `null`},
		"llm only":              {[]job.AnalyzerRun{run("techniques", job.RunFailed, ""), run("llm_techniques", job.RunSucceeded, llmResult)}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			j.Analyzers = c.runs
			got := x.view(t, j)["techniques"]
			if c.want != "" {
				require.JSONEq(t, c.want, string(got))
			} else {
				require.Contains(t, string(got), `"source":"llm"`)
			}
		})
	}
}

func TestPublicBotSignal(t *testing.T) {
	x := newPublic(t, nil)
	j := mkJob("b", 0, job.StatusFinalizing)
	for result, want := range map[string]string{
		`{"label": "bot_or_generated", "score": 0.87, "scope": "text", "model": "m"}`: `{"label": "bot_or_generated", "score": 0.87, "scope": "text"}`,
		`{"label": "human", "score": 0.1, "scope": "text"}`:                           `{"label": "human", "score": 0.1, "scope": "text"}`,
		`{"label": "human", "score": 0, "scope": "text"}`:                             `{"label": "human", "score": 0, "scope": "text"}`,
		`{"author": {"score": 0.9}}`:                                                  `null`,
		`{"label": "mixed", "score": 0.5, "scope": "text"}`:                           `null`,
		`{"label": "human", "score": 1.2, "scope": "text"}`:                           `null`,
		`{"label": "human", "score": -0.1, "scope": "text"}`:                          `null`,
		`{"label": "human", "score": 0.1}`:                                            `null`,
		`{"label": "human", "score": 0.1, "scope": "author"}`:                         `null`,
		`{"label": "human", "scope": "text"}`:                                         `null`,
		`{"score": 0.5, "scope": "text"}`:                                             `null`,
		`{"label": "human", "score": "0.1", "scope": "text"}`:                         `null`,
	} {
		j.Analyzers = []job.AnalyzerRun{run("bot_detection", job.RunSucceeded, result)}
		p := x.view(t, j)
		require.JSONEq(t, want, string(p["bot_signal"]), result)
		require.JSONEq(t, `null`, string(p["result"]), "partial summaries while finalizing")
	}
	j.Analyzers = []job.AnalyzerRun{run("bot_detection", job.RunFailed, "")}
	require.JSONEq(t, `null`, string(x.view(t, j)["bot_signal"]))
}

func TestPublicEvidence(t *testing.T) {
	x := newPublic(t, nil)
	j := mkJob("e", 0, job.StatusCompleted)
	j.Analyzers = []job.AnalyzerRun{
		run("fact_check", job.RunSucceeded, `{"queries": [], "requests_used": 1, "cached": 0, "fact_checks": [{"for_claim": "Die Rente mit 70 kommt",
		  "query": "q", "claim": "Rente mit 70 beschlossen", "claimant": "Facebook-Beitrag", "rating": "Falsch", "rating_normalized": "false",
		  "publisher": "Correctiv", "site": "correctiv.org", "url": "https://correctiv.org/x", "review_date": "2025-03-04T00:00:00Z"}]}`),
		run("news_search", job.RunSucceeded, `{"articles": [{"title": "T", "url": "https://n.example/a", "domain": "n.example", "source": null,
		  "date": null, "snippet": "s"}]}`),
		run("wikipedia", job.RunSucceeded, `{"languages": ["de", "en"], "claims": [
		  {"claim": "C1", "wikis": [{"language": "de", "status": "no_page", "title": null, "coverage": "no_coverage"},
		    {"language": "en", "status": "found", "title": "Retirement age", "url": "https://en.wikipedia.org/w/index.php?oldid=1",
		     "passage": "P1", "coverage": "aligned", "page_id": 3}]},
		  {"claim": "C2", "wikis": [{"language": "de", "status": "found", "title": "Rente", "url": "https://de.wikipedia.org/w/index.php?oldid=2",
		     "passage": "P2"}, {"language": "en", "status": "ambiguous"}]}]}`),
	}
	require.JSONEq(t, `{
	  "fact_checks": [{"claim": "Rente mit 70 beschlossen", "for_claim": "Die Rente mit 70 kommt", "rating": "Falsch", "rating_normalized": "false",
	    "publisher": "Correctiv", "url": "https://correctiv.org/x", "review_date": "2025-03-04T00:00:00Z"}],
	  "news": [{"title": "T", "url": "https://n.example/a", "domain": "n.example", "source": null, "date": null}],
	  "wikipedia": [
	    {"claim": "C1", "language": "en", "title": "Retirement age", "url": "https://en.wikipedia.org/w/index.php?oldid=1", "passage": "P1", "coverage": "aligned"},
	    {"claim": "C2", "language": "de", "title": "Rente", "url": "https://de.wikipedia.org/w/index.php?oldid=2", "passage": "P2", "coverage": null}]}`,
		string(x.view(t, j)["evidence"]))

	reason := job.ReasonDependencyFailed
	j.Analyzers = []job.AnalyzerRun{
		run("fact_check", job.RunSucceeded, `{"fact_checks": []}`),
		{ID: "news_search", Status: job.RunSkipped, Reason: &reason},
		run("wikipedia", job.RunSucceeded, `{"claims": [{"claim": "C", "wikis": [{"language": "en", "status": "no_page"}]}]}`),
	}
	require.JSONEq(t, `{"fact_checks": [], "news": null, "wikipedia": []}`, string(x.view(t, j)["evidence"]), "null: no lookup; []: nothing found")
}

func TestPublicResult(t *testing.T) {
	x := newPublic(t, nil)
	j := mkJob("r", 0, job.StatusCompleted)
	j.Finalization = job.Finalization{Status: job.StatusCompleted, Result: json.RawMessage(`{"verdict": "detected", "confidence": 0.86,
	  "user_text": "This post shows signs of propaganda.", "produced_by": "finalizer", "usage": {"tokens": 5}}`)}
	p := x.view(t, j)
	require.JSONEq(t, `"completed"`, string(p["finalization_status"]))
	require.JSONEq(t, `{"verdict": "detected", "confidence": 0.86, "user_text": "This post shows signs of propaganda.", "produced_by": "finalizer"}`, string(p["result"]))

	j.Finalization = job.Finalization{Status: job.RunFailed, Error: &job.RunError{Code: job.CodeTimeout, Message: "m"}}
	j.Analyzers = []job.AnalyzerRun{run("bot_detection", job.RunSucceeded, `{"label": "human", "score": 0.1, "scope": "text"}`)}
	p = x.view(t, j)
	require.JSONEq(t, `true`, string(p["done"]))
	require.JSONEq(t, `"failed"`, string(p["finalization_status"]))
	require.JSONEq(t, `null`, string(p["result"]))
	require.NotEqual(t, `null`, string(p["bot_signal"]), "other summaries still filled")
}

func TestPublicError(t *testing.T) {
	x := newPublic(t, nil)
	j := mkJob("s", 0, job.StatusFailed)
	j.Done = true
	j.Scrape = &job.Scrape{Status: job.RunFailed, ScrapeRequest: job.ScrapeRequest{URL: "https://bsky.app/x"},
		Error: &job.ScrapeError{Code: "account_private", Message: "the account is private", Details: json.RawMessage(`{"k":1}`)}}
	p := x.view(t, j)
	require.JSONEq(t, `"failed"`, string(p["status"]))
	require.JSONEq(t, `true`, string(p["done"]))
	require.JSONEq(t, `{"code": "account_private", "message": "the account is private"}`, string(p["error"]))

	j.Scrape = nil
	var e map[string]string
	require.NoError(t, json.Unmarshal(x.view(t, j)["error"], &e))
	require.Equal(t, "internal_error", e["code"])
	require.NotEmpty(t, e["message"])

	j.Status = job.StatusCompleted
	require.JSONEq(t, `null`, string(x.view(t, j)["error"]))
}

func TestPublicShareURL(t *testing.T) {
	x := newPublic(t, func(c *Config) { c.PublicBaseURL = "https://veil.example.org/" })
	for id, want := range map[string]string{"j_123": "https://veil.example.org/jobs/j_123", "a b/c?d": "https://veil.example.org/jobs/a%20b%2Fc%3Fd"} {
		x.put(mkJob(id, 0, job.StatusRunning))
		req := httptest.NewRequest("GET", "/api/v1/public/analyses/"+strings.NewReplacer(" ", "%20", "/", "%2F", "?", "%3F").Replace(id), nil)
		req.Host = "evil.example"
		req.Header.Set("X-Forwarded-Host", "evil.example")
		rec := httptest.NewRecorder()
		x.h.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		require.Equal(t, want, decode(t, rec)["share_url"])
	}
	require.JSONEq(t, `null`, string(newPublic(t, nil).view(t, mkJob("j_123", 0, job.StatusRunning))["share_url"]))
}

func TestPublicRateLimits(t *testing.T) {
	x := newPublic(t, func(c *Config) { c.PublicSubmitPerMinute, c.PublicGetPerMinute = 2, 1 })
	require.Equal(t, 201, x.do("POST", "/api/v1/public/analyses", `{"post":"a"}`).Code)
	require.Equal(t, 201, x.do("POST", "/api/v1/public/analyses", `{"post":"b"}`).Code)
	rec := x.do("POST", "/api/v1/public/analyses", `{"post":"c"}`)
	e := wantError(t, rec, 429, "rate_limited")
	require.Equal(t, "30", rec.Header().Get("Retry-After"), "ceil((1 - 0) / (2/60))")
	require.Equal(t, 30.0, e.Error.Details["retry_after_s"])
	require.Equal(t, int32(2), x.cs.creates.Load(), "only two jobs exist")
	// checked before the body is read: an oversize or invalid body still gets 429
	wantError(t, x.do("POST", "/api/v1/public/analyses", strings.Repeat("a", 2<<20)), 429, "rate_limited")
	wantError(t, x.do("POST", "/api/v1/public/analyses", ``), 429, "rate_limited")

	// jobs API unaffected while the submit limit is exhausted
	require.Equal(t, 201, x.do("POST", "/api/v1/jobs", `{"input":{"content":"x"}}`).Code)
	require.Equal(t, 200, x.do("GET", "/api/v1/jobs/job-1", "").Code)

	// polling does not consume submissions: get limit 1
	require.Equal(t, 200, x.do("GET", "/api/v1/public/analyses/job-1", "").Code)
	gets := x.cs.gets.Load()
	rec = x.do("GET", "/api/v1/public/analyses/job-1", "")
	wantError(t, rec, 429, "rate_limited")
	require.Equal(t, "60", rec.Header().Get("Retry-After"))
	require.Equal(t, gets, x.cs.gets.Load(), "a limited request does not read the job store")
	wantError(t, x.do("GET", "/api/v1/public/analyses/nope", ""), 429, "rate_limited")

	// refill: perMinute/60 tokens per second; retry_after_s is at least 1
	x.now = x.now.Add(29500 * time.Millisecond)
	rec = x.do("POST", "/api/v1/public/analyses", `{"post":"d"}`)
	wantError(t, rec, 429, "rate_limited")
	require.Equal(t, "1", rec.Header().Get("Retry-After"))
	x.now = x.now.Add(500 * time.Millisecond)
	require.Equal(t, 201, x.do("POST", "/api/v1/public/analyses", `{"post":"d"}`).Code)
	// capacity is the per-minute value: a long pause does not bank more than 2
	x.now = x.now.Add(time.Hour)
	require.Equal(t, 201, x.do("POST", "/api/v1/public/analyses", `{"post":"e"}`).Code)
	require.Equal(t, 201, x.do("POST", "/api/v1/public/analyses", `{"post":"f"}`).Code)
	wantError(t, x.do("POST", "/api/v1/public/analyses", `{"post":"g"}`), 429, "rate_limited")
	// submissions within the limit while gets are exhausted (and vice versa) work independently
	require.Equal(t, 200, x.do("GET", "/api/v1/public/analyses/job-1", "").Code)
}

func TestPublicGetLimitDoesNotBlockSubmit(t *testing.T) {
	x := newPublic(t, func(c *Config) { c.PublicGetPerMinute = 1 })
	x.put(mkJob("j", 0, job.StatusRunning))
	require.Equal(t, 200, x.do("GET", "/api/v1/public/analyses/j", "").Code)
	wantError(t, x.do("GET", "/api/v1/public/analyses/j", ""), 429, "rate_limited")
	require.Equal(t, 201, x.do("POST", "/api/v1/public/analyses", `{"post":"P"}`).Code)
}

func TestPublicDefaultLimits(t *testing.T) {
	x := newPublic(t, nil)
	for i := 0; i < DefaultPublicSubmitPerMinute; i++ {
		require.Equal(t, 400, x.do("POST", "/api/v1/public/analyses", `{}`).Code, "rejected requests count too")
	}
	wantError(t, x.do("POST", "/api/v1/public/analyses", `{"post":"x"}`), 429, "rate_limited")
}

func TestPublicRequestLog(t *testing.T) {
	x := newPublic(t, nil)
	x.do("POST", "/api/v1/public/analyses?token=q-s3cr3t", `{"post":"body-s3cr3t"}`)
	x.do("GET", "/api/v1/public/analyses/job-1?k=q-s3cr3t", "")
	logs := x.logs.String()
	require.Contains(t, logs, `"path":"/api/v1/public/analyses"`)
	require.Contains(t, logs, `"path":"/api/v1/public/analyses/job-1"`)
	require.NotContains(t, logs, "s3cr3t")
}
