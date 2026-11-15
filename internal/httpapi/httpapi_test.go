package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
	"github.com/junction-2026/backend/internal/store"
)

// fakeSvc is the fake analyzer service behind the handlers.
type fakeSvc struct {
	mu     sync.Mutex
	jobs   map[string]job.Job // Get/List/Cancel source
	create func(engine.CreateRequest) (job.Job, error)
	last   engine.CreateRequest
	bus    *events.Bus
	subbed chan struct{} // closed on the first Subscribe
	maps   *store.Memory // map source (map_test.go); nil: no map analyses
	once   sync.Once
}

func (f *fakeSvc) Create(r engine.CreateRequest) (job.Job, error) {
	f.mu.Lock()
	f.last = r
	f.mu.Unlock()
	return f.create(r)
}

func (f *fakeSvc) Get(id string) (job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j, ok := f.jobs[id]; ok {
		return j, nil
	}
	return job.Job{}, engine.ErrNotFound
}

func (f *fakeSvc) Cancel(id string) (job.Job, error) {
	j, err := f.Get(id)
	if err != nil {
		return j, err
	}
	if j.Done {
		return job.Job{}, engine.ErrJobFinished
	}
	j.Status, j.Done = job.StatusCancelled, true
	return j, nil
}

func (f *fakeSvc) Page(status, q string, after *store.Cursor, limit int) ([]job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []job.Job
	for _, j := range f.jobs {
		if !j.Public || (status != "" && j.Status != status) || !store.Matches(&j, strings.ToLower(q)) {
			continue
		}
		// newest first, ties by id (the engine's contract for Page)
		if after == nil || j.CreatedAt.Before(after.CreatedAt) || (j.CreatedAt.Equal(after.CreatedAt) && j.ID > after.ID) {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].CreatedAt.Equal(out[b].CreatedAt) {
			return out[a].CreatedAt.After(out[b].CreatedAt)
		}
		return out[a].ID < out[b].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeSvc) MapCountries() ([]store.MapCountry, error) {
	if f.maps == nil {
		return nil, nil
	}
	return f.maps.MapCountries()
}

func (f *fakeSvc) MapAnalyses(code string, after *store.Cursor, limit int) ([]store.MapItem, error) {
	if f.maps == nil {
		return nil, nil
	}
	return f.maps.MapAnalyses(code, after, limit)
}

func (f *fakeSvc) Subscribe(id string) (*events.Subscription, error) {
	if f.bus == nil {
		return nil, engine.ErrNotFound
	}
	defer f.once.Do(func() { close(f.subbed) })
	return f.bus.Subscribe(id, func() any { return map[string]any{"job": job.Job{ID: id}} }), nil
}

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func mkJob(id string, age int, status string) job.Job {
	return job.Job{ID: id, Public: true, Status: status, Done: status == job.StatusCompleted, CreatedAt: t0.Add(-time.Duration(age) * time.Second),
		Analyzers: []job.AnalyzerRun{}, Finalization: job.Finalization{Status: job.RunPending}}
}

func newFake() *fakeSvc {
	f := &fakeSvc{jobs: map[string]job.Job{}, subbed: make(chan struct{})}
	f.create = func(r engine.CreateRequest) (job.Job, error) {
		j := mkJob("job-1", 0, job.StatusQueued)
		j.Public = !r.Private
		if r.Scrape != nil {
			j.Scrape = &job.Scrape{Status: job.RunPending, ScrapeRequest: *r.Scrape}
			return j, nil
		}
		in := r.Input
		j.Input = &in
		if j.Input.ContentType == "" {
			j.Input.ContentType = "text/plain"
		}
		return j, nil
	}
	return f
}

const regYAMLText = `analyzers:
  - id: claims
    name: Claims
    category: facts
    depends_on: [entities]
    ui: {hidden: true, activity_key: fact_check} # never exposed
    execution: {adapter: custom, handler: h}
  - id: entities
    execution: {adapter: custom, handler: h}
  - id: legacy
    enabled: false
    execution: {adapter: custom, handler: h}
  - id: final_synthesis
    trigger: after_all_analyzers_settled
    execution:
      adapter: http_sync
      endpoint: ${FINALIZER_URL}
      headers: {Authorization: "Bearer ${FINALIZER_TOKEN}"}
`

func loadReg(t *testing.T, yaml string, env map[string]string) *registry.Registry {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r.yaml")
	require.NoError(t, os.WriteFile(p, []byte(yaml), 0o600))
	reg, err := registry.Load(p, registry.Options{
		Getenv:        func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		HandlerExists: func(string) bool { return true },
	})
	require.NoError(t, err)
	return reg
}

// syncBuf is a log sink safe for the request goroutine and the test.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type harness struct {
	h    http.Handler
	svc  *fakeSvc
	logs *syncBuf
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	svc := newFake()
	logs := &syncBuf{}
	reg := loadReg(t, regYAMLText, map[string]string{"FINALIZER_URL": "https://secret-host.example/final?key=s3cr3t", "FINALIZER_TOKEN": "tok-s3cr3t"})
	events := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.PathValue("id")) })
	h := NewHandler(Config{Service: svc, Registry: reg, Events: events, Logger: slog.New(slog.NewJSONHandler(logs, nil))})
	return &harness{h: h, svc: svc, logs: logs}
}

func (x *harness) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	x.h.ServeHTTP(rec, req)
	return rec
}

type errResp struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// wantError asserts status and the api-conventions envelope.
func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) errResp {
	t.Helper()
	require.Equal(t, status, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var e errResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
	require.Equal(t, code, e.Error.Code)
	require.NotEmpty(t, e.Error.Message)
	return e
}

func TestHealthz(t *testing.T) {
	rec := newHarness(t).do("GET", "/healthz", "")
	require.Equal(t, 200, rec.Code)
	require.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}

func TestCreateJob(t *testing.T) {
	x := newHarness(t)
	rec := x.do("POST", "/api/v1/jobs", `{"input":{"content":"hello"},"priority":1,"input2":{}}`)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	require.Equal(t, "/api/v1/jobs/job-1", rec.Header().Get("Location"))
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "job-1", got["id"])
	require.Equal(t, "hello", got["input"].(map[string]any)["content"])
	require.Equal(t, "text/plain", got["input"].(map[string]any)["content_type"])
	require.Nil(t, x.svc.last.Analyzers, "omitted analyzers selects all")
	require.Nil(t, x.svc.last.Finalize)
}

func TestCreateJobParsesAllMembers(t *testing.T) {
	x := newHarness(t)
	rec := x.do("POST", "/api/v1/jobs", `{"input":{"content":"x","content_type":"Text/HTML; charset=utf-8","source":{
	  "url":"https://example.com/a","title":"T","author":"A","platform":"P","published_at":"2026-11-14T10:00:00.5+02:00",
	  "metadata":{"b":1,  "a":[true]}}},"analyzers":[],"options":{"finalize":false}}`)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	r := x.svc.last
	require.NotNil(t, r.Analyzers)
	require.Empty(t, r.Analyzers, "empty list is a non-nil empty selection")
	require.False(t, *r.Finalize)
	require.Equal(t, "Text/HTML; charset=utf-8", r.Input.ContentType)
	s := r.Input.Source
	require.Equal(t, "https://example.com/a", s.URL)
	require.Equal(t, "T", s.Title)
	require.Equal(t, "A", s.Author)
	require.Equal(t, "P", s.Platform)
	require.True(t, s.PublishedAt.Equal(time.Date(2026, 11, 14, 8, 0, 0, 500_000_000, time.UTC)))
	require.JSONEq(t, `{"b":1,"a":[true]}`, string(s.Metadata))

	x.do("POST", "/api/v1/jobs", `{"input":{"content":"x"},"analyzers":["b","a"]}`)
	require.Equal(t, []string{"b", "a"}, x.svc.last.Analyzers)
}

func TestCreateJobInvalidRequest(t *testing.T) {
	x := newHarness(t)
	for name, body := range map[string]string{
		"truncated": `{"input":`,
		"empty":     ``,
		"not json":  `hello`,
		"trailing":  `{"input":{"content":"x"}} {}`,
	} {
		t.Run(name, func(t *testing.T) { wantError(t, x.do("POST", "/api/v1/jobs", body), 400, "invalid_request") })
	}
}

func TestCreateJobValidation(t *testing.T) {
	x := newHarness(t)
	cases := []struct{ name, body, field string }{
		{"content wrong type", `{"input":{"content":5}}`, "input.content"},
		{"blank content", `{"input":{"content":"   \n"}}`, "input.content"},
		{"missing content", `{"input":{}}`, "input.content"},
		{"null content", `{"input":{"content":null}}`, "input.content"},
		{"missing input", `{}`, "input"},
		{"input wrong type", `{"input":"x"}`, "input"},
		{"body not object", `[1]`, "body"},
		{"content_type unsupported", `{"input":{"content":"x","content_type":"image/png"}}`, "input.content_type"},
		{"content_type wrong type", `{"input":{"content":"x","content_type":1}}`, "input.content_type"},
		{"relative url", `{"input":{"content":"x","source":{"url":"/a/b"}}}`, "input.source.url"},
		{"ftp url", `{"input":{"content":"x","source":{"url":"ftp://h/x"}}}`, "input.source.url"},
		{"bad published_at", `{"input":{"content":"x","source":{"published_at":"yesterday"}}}`, "input.source.published_at"},
		{"source wrong type", `{"input":{"content":"x","source":[]}}`, "input.source"},
		{"title wrong type", `{"input":{"content":"x","source":{"title":3}}}`, "input.source.title"},
		{"metadata wrong type", `{"input":{"content":"x","source":{"metadata":"m"}}}`, "input.source.metadata"},
		{"duplicate analyzers", `{"input":{"content":"x"},"analyzers":["a","b","a"]}`, "analyzers"},
		{"analyzers wrong type", `{"input":{"content":"x"},"analyzers":"a"}`, "analyzers"},
		{"options wrong type", `{"input":{"content":"x"},"options":1}`, "options"},
		{"finalize wrong type", `{"input":{"content":"x"},"options":{"finalize":"yes"}}`, "options.finalize"},
		{"input and scrape", `{"input":{"content":"x"},"scrape":{"url":"https://x.com/alice"}}`, "input"},
		{"scrape wrong type", `{"scrape":"https://x.com/alice"}`, "scrape"},
		{"scrape url missing", `{"scrape":{}}`, "scrape.url"},
		{"scrape url wrong type", `{"scrape":{"url":5}}`, "scrape.url"},
		{"scrape url ftp", `{"scrape":{"url":"ftp://example.com/a"}}`, "scrape.url"},
		{"scrape url relative", `{"scrape":{"url":"/alice"}}`, "scrape.url"},
		{"scrape url too long", `{"scrape":{"url":"https://x.com/` + strings.Repeat("a", 2048) + `"}}`, "scrape.url"},
		{"max_posts zero", `{"scrape":{"url":"https://x.com/a","max_posts":0}}`, "scrape.max_posts"},
		{"max_posts too large", `{"scrape":{"url":"https://x.com/a","max_posts":101}}`, "scrape.max_posts"},
		{"max_posts fraction", `{"scrape":{"url":"https://x.com/a","max_posts":1.5}}`, "scrape.max_posts"},
		{"max_comments negative", `{"scrape":{"url":"https://x.com/a","max_comments":-1}}`, "scrape.max_comments"},
		{"max_comments too large", `{"scrape":{"url":"https://x.com/a","max_comments":201}}`, "scrape.max_comments"},
		{"max_comments wrong type", `{"scrape":{"url":"https://x.com/a","max_comments":"5"}}`, "scrape.max_comments"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := wantError(t, x.do("POST", "/api/v1/jobs", c.body), 422, "validation_failed")
			require.Contains(t, e.Error.Details["fields"], c.field)
		})
	}
	t.Run("all violations are reported together", func(t *testing.T) {
		e := wantError(t, x.do("POST", "/api/v1/jobs", `{"input":{"content":" ","content_type":"x/y"},"options":{"finalize":1}}`), 422, "validation_failed")
		require.Len(t, e.Error.Details["fields"], 3)
	})
}

func TestCreateScrapeJob(t *testing.T) {
	x := newHarness(t)
	rec := x.do("POST", "/api/v1/jobs", `{"scrape":{"url":"https://bsky.app/profile/alice.bsky.social/post/3kxyz"},"input":null}`)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	require.Equal(t, &job.ScrapeRequest{URL: "https://bsky.app/profile/alice.bsky.social/post/3kxyz", MaxPosts: 20, MaxComments: 50}, x.svc.last.Scrape)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Nil(t, got["input"])
	require.Equal(t, "pending", got["scrape"].(map[string]any)["status"])
	require.EqualValues(t, 20, got["scrape"].(map[string]any)["max_posts"])

	x.do("POST", "/api/v1/jobs", `{"scrape":{"url":"https://x.com/a","max_posts":100,"max_comments":0}}`)
	require.Equal(t, &job.ScrapeRequest{URL: "https://x.com/a", MaxPosts: 100, MaxComments: 0}, x.svc.last.Scrape)

	rec = x.do("POST", "/api/v1/jobs", `{"input":{"content":"hello"}}`)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Contains(t, got, "scrape")
	require.Nil(t, got["scrape"], "text jobs carry scrape: null")

	x.svc.create = func(engine.CreateRequest) (job.Job, error) { return job.Job{}, engine.ErrScrapeUnavailable }
	wantError(t, x.do("POST", "/api/v1/jobs", `{"scrape":{"url":"https://x.com/alice"}}`), 503, "scrape_unavailable")
	require.Equal(t, 200, x.do("GET", "/api/v1/jobs?status=scraping", "").Code)
}

func TestCreateJobTooLarge(t *testing.T) {
	x := newHarness(t)
	pad := strings.Repeat("a", maxBody)
	wantError(t, x.do("POST", "/api/v1/jobs", `{"input":{"content":"`+pad+`"}}`), 413, "payload_too_large")

	// exactly 1 MiB is accepted by the limit
	prefix, suffix := `{"input":{"content":"`, `"}}`
	body := prefix + strings.Repeat("a", maxBody-len(prefix)-len(suffix)) + suffix
	require.Len(t, body, maxBody)
	require.Equal(t, 201, x.do("POST", "/api/v1/jobs", body).Code)
}

func TestCreateJobEngineErrors(t *testing.T) {
	x := newHarness(t)
	body := `{"input":{"content":"x"},"analyzers":["nope"]}`

	x.svc.create = func(engine.CreateRequest) (job.Job, error) {
		return job.Job{}, &engine.UnknownAnalyzerError{IDs: []string{"nope"}}
	}
	e := wantError(t, x.do("POST", "/api/v1/jobs", body), 422, "unknown_analyzer")
	require.Equal(t, []any{"nope"}, e.Error.Details["analyzer_ids"])

	x.svc.create = func(engine.CreateRequest) (job.Job, error) {
		return job.Job{}, &engine.NotSelectableError{IDs: []string{"final_synthesis"}}
	}
	e = wantError(t, x.do("POST", "/api/v1/jobs", body), 422, "analyzer_not_selectable")
	require.Equal(t, []any{"final_synthesis"}, e.Error.Details["analyzer_ids"])

	x.svc.create = func(engine.CreateRequest) (job.Job, error) {
		return job.Job{}, errors.New("dial https://secret-host.example/x failed")
	}
	rec := x.do("POST", "/api/v1/jobs", body)
	wantError(t, rec, 500, "internal_error")
	require.NotContains(t, rec.Body.String(), "secret-host")
	require.NotContains(t, x.logs.String(), "secret-host")
}

func TestGetAndCancelJob(t *testing.T) {
	x := newHarness(t)
	x.svc.jobs["run"] = mkJob("run", 0, job.StatusRunning)
	x.svc.jobs["fin"] = mkJob("fin", 0, job.StatusCompleted)

	rec := x.do("GET", "/api/v1/jobs/run", "")
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), `"id":"run"`)
	wantError(t, x.do("GET", "/api/v1/jobs/missing", ""), 404, "not_found")

	rec = x.do("POST", "/api/v1/jobs/run/cancel", "")
	require.Equal(t, 202, rec.Code)
	require.Contains(t, rec.Body.String(), `"status":"cancelled"`)
	require.Contains(t, rec.Body.String(), `"done":true`)
	wantError(t, x.do("POST", "/api/v1/jobs/fin/cancel", ""), 409, "job_finished")
	wantError(t, x.do("POST", "/api/v1/jobs/missing/cancel", ""), 404, "not_found")
}

type page struct {
	Items      []JobSummary `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

func list(t *testing.T, x *harness, query string) page {
	t.Helper()
	rec := x.do("GET", "/api/v1/jobs"+query, "")
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var p page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	return p
}

func TestListJobsPagination(t *testing.T) {
	x := newHarness(t)
	// c and d share created_at: ties are ordered by id
	for id, age := range map[string]int{"a": 0, "b": 1, "c": 2, "d": 2, "e": 3} {
		x.svc.jobs[id] = mkJob(id, age, job.StatusRunning)
	}
	var seen []string
	q := "?limit=2"
	for pages := 0; ; pages++ {
		require.Less(t, pages, 5)
		p := list(t, x, q)
		for _, it := range p.Items {
			seen = append(seen, it.ID)
		}
		if p.NextCursor == nil {
			require.LessOrEqual(t, len(p.Items), 2)
			break
		}
		require.Len(t, p.Items, 2)
		q = "?limit=2&cursor=" + *p.NextCursor
	}
	require.Equal(t, []string{"a", "b", "c", "d", "e"}, seen)

	require.Nil(t, list(t, x, "?limit=5").NextCursor, "exactly limit items remaining: last page")
	require.Len(t, list(t, x, "").Items, 5)
}

func TestListJobsEmptyIsArray(t *testing.T) {
	x := newHarness(t)
	rec := x.do("GET", "/api/v1/jobs", "")
	require.JSONEq(t, `{"items":[],"next_cursor":null}`, rec.Body.String())
}

func TestListJobsCursorIsOpaqueAndStable(t *testing.T) {
	x := newHarness(t)
	for i, id := range []string{"a", "b", "c"} {
		x.svc.jobs[id] = mkJob(id, i, job.StatusRunning)
	}
	p := list(t, x, "?limit=1")
	raw, err := base64.RawURLEncoding.DecodeString(*p.NextCursor)
	require.NoError(t, err)
	require.Equal(t, "2026-11-14T10:00:00Z|a", string(raw))
	// a job created after the first page does not shift the second page
	x.svc.jobs["z"] = mkJob("z", -5, job.StatusRunning)
	require.Equal(t, "b", list(t, x, "?limit=1&cursor="+*p.NextCursor).Items[0].ID)
}

func TestListJobsFilterAndSummary(t *testing.T) {
	x := newHarness(t)
	done := mkJob("done", 1, job.StatusCompleted)
	done.Finalization = job.Finalization{Status: job.StatusCompleted, Result: json.RawMessage(`{"verdict":"detected","confidence":0.8,"user_text":"x"}`)}
	x.svc.jobs["done"] = done
	x.svc.jobs["run"] = mkJob("run", 0, job.StatusRunning)

	p := list(t, x, "?status=completed")
	require.Len(t, p.Items, 1)
	it := p.Items[0]
	require.Equal(t, "done", it.ID)
	require.True(t, it.Done)
	require.Equal(t, "completed", it.Finalization.Status)
	require.Equal(t, "detected", *it.Finalization.Verdict)
	require.Nil(t, list(t, x, "?status=running").Items[0].Finalization.Verdict)

	rec := x.do("GET", "/api/v1/jobs?status=running", "")
	require.Contains(t, rec.Body.String(), `"finalization":{"status":"pending","verdict":null}`)
	require.NotContains(t, rec.Body.String(), `"input"`, "summaries are not full jobs")
}

func TestListJobsInvalidParameters(t *testing.T) {
	x := newHarness(t)
	notB64 := "%21%21"
	noPipe := base64.RawURLEncoding.EncodeToString([]byte("garbage"))
	badTime := base64.RawURLEncoding.EncodeToString([]byte("yesterday|id"))
	for _, q := range []string{"limit=0", "limit=101", "limit=500", "limit=abc", "limit=-1", "status=bogus",
		"cursor=" + notB64, "cursor=" + noPipe, "cursor=" + badTime, "cursor=" + base64.URLEncoding.EncodeToString([]byte("2026-11-14T10:00:00Z|a"))} {
		t.Run(q, func(t *testing.T) { wantError(t, x.do("GET", "/api/v1/jobs?"+q, ""), 400, "invalid_request") })
	}
	require.Equal(t, 200, x.do("GET", "/api/v1/jobs?limit=100", "").Code)
	require.Equal(t, 200, x.do("GET", "/api/v1/jobs?limit=1", "").Code)
}

func TestListAnalyzersHidesExecutionSettings(t *testing.T) {
	x := newHarness(t)
	rec := x.do("GET", "/api/v1/analyzers", "")
	require.Equal(t, 200, rec.Code)
	body := rec.Body.String()
	for _, secret := range []string{"secret-host", "s3cr3t", "tok-", "Authorization", "endpoint", "headers", "https://", "handler"} {
		require.NotContains(t, body, secret)
	}
	var got struct{ Items []map[string]any }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []map[string]any{
		{"id": "claims", "name": "Claims", "category": "facts", "enabled": true, "depends_on": []any{"entities"}, "trigger": "on_dependencies_succeeded", "selectable": true},
		{"id": "entities", "name": "entities", "category": "", "enabled": true, "depends_on": []any{}, "trigger": "on_dependencies_succeeded", "selectable": true},
		{"id": "final_synthesis", "name": "final_synthesis", "category": "", "enabled": true, "depends_on": []any{}, "trigger": "after_all_analyzers_settled", "selectable": false},
		{"id": "legacy", "name": "legacy", "category": "", "enabled": false, "depends_on": []any{}, "trigger": "on_dependencies_succeeded", "selectable": true},
	}, got.Items)
}

func TestRoutingErrorsUseEnvelope(t *testing.T) {
	x := newHarness(t)
	wantError(t, x.do("GET", "/nope", ""), 404, "not_found")
	wantError(t, x.do("GET", "/api/v1/nope", ""), 404, "not_found")
	wantError(t, x.do("GET", "/api/v1/jobs/a/b/c", ""), 404, "not_found")
	for path, allow := range map[string]string{
		"/api/v1/jobs":                "GET, POST",
		"/api/v1/jobs/x":              "GET",
		"/api/v1/jobs/x/cancel":       "POST",
		"/api/v1/jobs/x/localization": "GET",
		"/api/v1/analyzers":           "GET",
		"/api/v1/jobs/x/events":       "GET",
		"/healthz":                    "GET",
	} {
		rec := x.do("DELETE", path, "")
		wantError(t, rec, 405, "method_not_allowed")
		require.Equal(t, allow, rec.Header().Get("Allow"), path)
	}
	require.Equal(t, "abc", x.do("GET", "/api/v1/jobs/abc/events", "").Body.String(), "events handler gets the job id")
}

func TestPanicBecomesInternalError(t *testing.T) {
	x := newHarness(t)
	x.svc.create = func(engine.CreateRequest) (job.Job, error) { panic("boom secret-host") }
	rec := x.do("POST", "/api/v1/jobs", `{"input":{"content":"x"}}`)
	wantError(t, rec, 500, "internal_error")
	require.NotContains(t, rec.Body.String(), "boom")
	require.NotContains(t, x.logs.String(), "secret-host")
}

func TestLogsCarryIDsNotContent(t *testing.T) {
	x := newHarness(t)
	x.svc.bus = events.New(16)
	x.svc.jobs["job-1"] = mkJob("job-1", 0, job.StatusRunning)

	rec := x.do("POST", "/api/v1/jobs?token=qs-secret", `{"input":{"content":"BODY-SECRET"}}`)
	require.Equal(t, 201, rec.Code)
	// the logging subscriber is attached asynchronously: publish until it saw the settle
	url := "https://secret-host.example/analyze?key=s3cr3t"
	failed := job.AnalyzerRun{ID: "claims", Status: job.RunFailed, DurationMS: job.Ptr[int64](12),
		Error: &job.RunError{Code: job.CodeAdapterError, Message: "POST " + url + " refused"}}
	ok := job.AnalyzerRun{ID: "entities", Status: job.RunSucceeded, Result: json.RawMessage(`{"leak":"RESULT-SECRET"}`)}
	select {
	case <-x.svc.subbed:
	case <-time.After(2 * time.Second):
		t.Fatal("logging subscriber never attached")
	}
	x.svc.bus.Publish("job-1", "analyzer.failed", map[string]any{"analyzer": failed})
	x.svc.bus.Publish("job-1", "analyzer.succeeded", map[string]any{"analyzer": ok})
	x.svc.bus.Publish("job-1", "job.completed", map[string]any{"job": job.Job{ID: "job-1", Status: job.StatusCompleted}})
	require.Eventually(t, func() bool { return strings.Contains(x.logs.String(), `"job finished"`) }, 2*time.Second, 5*time.Millisecond)

	logs := x.logs.String()
	require.Contains(t, logs, `"job_id":"job-1"`)
	require.Contains(t, logs, `"analyzer_id":"claims"`)
	require.Contains(t, logs, `"error_code":"adapter_error"`)
	require.Contains(t, logs, `"analyzer_id":"entities"`)
	for _, leak := range []string{"BODY-SECRET", "RESULT-SECRET", "secret-host", "s3cr3t", "qs-secret", "refused"} {
		require.NotContains(t, logs, leak)
	}
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		require.True(t, json.Valid([]byte(line)), "structured JSON lines: %s", line)
	}
}

// A techniques result stored before detection strength (no margin, located, strength or summary) is served unchanged.
func TestGetJobServesTechniquesResultWithoutStrength(t *testing.T) {
	x := newHarness(t)
	old := `{"words":3,"detected":[{"name":"Doubt","display":"Doubt","explanation":"x","models":["nci-social"],"spans":[]}]}`
	j := mkJob("old", 0, job.StatusCompleted)
	j.Analyzers = []job.AnalyzerRun{{ID: "techniques", Status: job.RunSucceeded, Result: json.RawMessage(old)}}
	x.svc.jobs["old"] = j
	rec := x.do("GET", "/api/v1/jobs/old", "")
	require.Equal(t, 200, rec.Code)
	var got struct {
		Analyzers []struct{ Result json.RawMessage }
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.JSONEq(t, old, string(got.Analyzers[0].Result))
}

func TestCreateJobPublicFlag(t *testing.T) {
	x := newHarness(t)
	scrape := `"scrape":{"url":"https://bsky.app/profile/alice.bsky.social/post/3kxyz"}`
	for _, c := range []struct {
		body   string
		public bool
	}{
		{`{"input":{"content":"x"}}`, true},
		{`{"input":{"content":"x"},"public":true}`, true},
		{`{"input":{"content":"x"},"public":false}`, false},
		{`{` + scrape + `}`, true},
		{`{` + scrape + `,"public":false}`, false},
	} {
		rec := x.do("POST", "/api/v1/jobs", c.body)
		require.Equal(t, 201, rec.Code, c.body+rec.Body.String())
		var got map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		require.Equal(t, c.public, got["public"], c.body)
	}
	for _, v := range []string{`"no"`, `1`, `null`, `{}`, `[]`} {
		for _, body := range []string{`{"input":{"content":"x"},"public":` + v + `}`, `{` + scrape + `,"public":` + v + `}`} {
			x.svc.last = engine.CreateRequest{}
			e := wantError(t, x.do("POST", "/api/v1/jobs", body), 422, "validation_failed")
			require.Contains(t, e.Error.Details["fields"], "public", body)
			require.Empty(t, x.svc.last, "no job is created: "+body)
		}
	}
	// reported next to the other violations
	e := wantError(t, x.do("POST", "/api/v1/jobs", `{"input":{"content":" "},"public":"no"}`), 422, "validation_failed")
	require.Contains(t, e.Error.Details["fields"], "public")
	require.Contains(t, e.Error.Details["fields"], "input.content")
}

func TestListJobsExcludesNonPublic(t *testing.T) {
	x := newHarness(t)
	for i, id := range []string{"p1", "u1", "p2", "u2", "p3"} {
		j := mkJob(id, 10-i, job.StatusRunning)
		j.Public = id[0] == 'p'
		x.svc.jobs[id] = j
	}
	for _, q := range []string{"?limit=2", "?status=running&limit=2"} {
		p := list(t, x, q)
		require.Len(t, p.Items, 2)
		require.NotNil(t, p.NextCursor)
		p2 := list(t, x, q+"&cursor="+*p.NextCursor)
		require.Len(t, p2.Items, 1)
		require.Nil(t, p2.NextCursor)
		require.Equal(t, "p3", p.Items[0].ID)
		require.Equal(t, "p2", p.Items[1].ID)
		require.Equal(t, "p1", p2.Items[0].ID)
	}
	require.Equal(t, 200, x.do("GET", "/api/v1/jobs/u1", "").Code, "unlisted jobs are read by id")
}
