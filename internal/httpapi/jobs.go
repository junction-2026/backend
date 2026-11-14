package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/localization"
	"github.com/junction-2026/backend/internal/registry"
	"github.com/junction-2026/backend/internal/store"
)

var (
	contentTypes = map[string]bool{"text/plain": true, "text/markdown": true, "text/html": true}
	jobStatuses  = map[string]bool{job.StatusQueued: true, job.StatusScraping: true, job.StatusRunning: true, job.StatusFinalizing: true,
		job.StatusCompleted: true, job.StatusCancelled: true, job.StatusFailed: true}
)

// fields collects validation violations by dotted member path.
type fields map[string]any

func object(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	return m, json.Unmarshal(raw, &m) == nil && m != nil
}

// member returns the raw value of key, absent for a missing member or null.
func member(m map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	raw, ok := m[key]
	return raw, ok && string(bytes.TrimSpace(raw)) != "null"
}

// str reads an optional string member; a wrong type is recorded under path.
func (f fields) str(m map[string]json.RawMessage, key, path string) (s string, ok bool) {
	raw, present := member(m, key)
	if !present {
		return "", false
	}
	if json.Unmarshal(raw, &s) != nil {
		f[path] = "must be a string"
		return "", false
	}
	return s, true
}

// optObject reads an optional object member.
func (f fields) optObject(m map[string]json.RawMessage, key, path string) (map[string]json.RawMessage, bool) {
	raw, present := member(m, key)
	if !present {
		return nil, false
	}
	o, ok := object(raw)
	if !ok {
		f[path] = "must be an object"
	}
	return o, ok
}

// parseCreate validates the POST /jobs body (valid JSON, any shape).
func parseCreate(body json.RawMessage) (engine.CreateRequest, fields) {
	var req engine.CreateRequest
	f := fields{}
	top, ok := object(body)
	if !ok {
		f["body"] = "must be a JSON object"
		return req, f
	}

	_, hasInput := member(top, "input")
	if sc, hasScrape := member(top, "scrape"); hasScrape {
		if hasInput {
			f["input"] = "exactly one of input and scrape is allowed"
		}
		req.Scrape = parseScrape(sc, f)
	} else if in, ok := f.optObject(top, "input", "input"); ok {
		content, present := f.str(in, "content", "input.content")
		switch {
		case !present && f["input.content"] == nil:
			f["input.content"] = "is required"
		case present && strings.TrimSpace(content) == "":
			f["input.content"] = "must not be blank"
		}
		req.Input.Content = content
		if ct, ok := f.str(in, "content_type", "input.content_type"); ok {
			mt := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
			if !contentTypes[mt] {
				f["input.content_type"] = "must be one of text/plain, text/markdown, text/html"
			}
			req.Input.ContentType = ct
		}
		if src, ok := f.optObject(in, "source", "input.source"); ok {
			parseSource(src, &req.Input.Source, f)
		}
	} else if f["input"] == nil {
		f["input"] = "input or scrape is required"
	}

	if raw, present := member(top, "analyzers"); present {
		var ids []string
		if json.Unmarshal(raw, &ids) != nil {
			f["analyzers"] = "must be an array of analyzer ids"
		} else {
			seen, dups := map[string]bool{}, []string{}
			for _, id := range ids {
				if seen[id] {
					dups = append(dups, id)
				}
				seen[id] = true
			}
			if len(dups) > 0 {
				f["analyzers"] = "duplicate ids: " + strings.Join(dups, ", ")
			}
			req.Analyzers = append([]string{}, ids...) // non-nil: an empty list selects nothing
		}
	}
	if opt, ok := f.optObject(top, "options", "options"); ok {
		if raw, present := member(opt, "finalize"); present {
			var b bool
			if json.Unmarshal(raw, &b) != nil {
				f["options.finalize"] = "must be a boolean"
			} else {
				req.Finalize = &b
			}
		}
	}
	return req, f
}

// parseScrape validates a ScrapeRequest and applies its defaults.
func parseScrape(raw json.RawMessage, f fields) *job.ScrapeRequest {
	m, ok := object(raw)
	if !ok {
		f["scrape"] = "must be an object"
		return nil
	}
	sr := &job.ScrapeRequest{MaxPosts: 20, MaxComments: 50}
	if v, ok := f.str(m, "url", "scrape.url"); !ok {
		if f["scrape.url"] == nil {
			f["scrape.url"] = "is required"
		}
	} else if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(v) > 2048 {
		f["scrape.url"] = "must be an absolute http(s) URL of at most 2048 characters"
	} else {
		sr.URL = v
	}
	f.intRange(m, "max_posts", "scrape.max_posts", 1, 100, &sr.MaxPosts)
	f.intRange(m, "max_comments", "scrape.max_comments", 0, 200, &sr.MaxComments)
	return sr
}

// intRange reads an optional integer member in [lo, hi] into dst.
func (f fields) intRange(m map[string]json.RawMessage, key, path string, lo, hi int, dst *int) {
	raw, present := member(m, key)
	if !present {
		return
	}
	var n int
	if json.Unmarshal(raw, &n) != nil || n < lo || n > hi {
		f[path] = fmt.Sprintf("must be an integer between %d and %d", lo, hi)
		return
	}
	*dst = n
}

func parseSource(m map[string]json.RawMessage, s *job.Source, f fields) {
	if v, ok := f.str(m, "url", "input.source.url"); ok {
		if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			f["input.source.url"] = "must be an absolute http(s) URL"
		}
		s.URL = v
	}
	s.Title, _ = f.str(m, "title", "input.source.title")
	s.Author, _ = f.str(m, "author", "input.source.author")
	s.Platform, _ = f.str(m, "platform", "input.source.platform")
	if v, ok := f.str(m, "published_at", "input.source.published_at"); ok {
		if t, err := time.Parse(time.RFC3339, v); err != nil {
			f["input.source.published_at"] = "must be an RFC 3339 timestamp"
		} else {
			s.PublishedAt = &t
		}
	}
	if _, ok := f.optObject(m, "metadata", "input.source.metadata"); ok {
		s.Metadata = m["metadata"] // free-form, kept verbatim
	}
}

func (a *api) createJob(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds 1 MiB", nil)
		} else {
			WriteError(w, http.StatusBadRequest, "invalid_request", "could not read request body", nil)
		}
		return
	}
	var raw json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&raw); err != nil || dec.More() {
		WriteError(w, http.StatusBadRequest, "invalid_request", "request body is not valid JSON", nil)
		return
	}
	req, f := parseCreate(raw)
	if len(f) > 0 {
		WriteError(w, http.StatusUnprocessableEntity, "validation_failed", "request body violates the schema", map[string]any{"fields": f})
		return
	}
	j, err := a.svc.Create(req)
	var unknown *engine.UnknownAnalyzerError
	var notSel *engine.NotSelectableError
	switch {
	case errors.As(err, &unknown):
		WriteError(w, http.StatusUnprocessableEntity, "unknown_analyzer", "unknown analyzer id", map[string]any{"analyzer_ids": unknown.IDs})
	case errors.Is(err, engine.ErrScrapeUnavailable):
		WriteError(w, http.StatusServiceUnavailable, "scrape_unavailable", "no scraper is configured on this server", nil)
	case errors.As(err, &notSel):
		WriteError(w, http.StatusUnprocessableEntity, "analyzer_not_selectable", "analyzer cannot be selected", map[string]any{"analyzer_ids": notSel.IDs})
	case err != nil:
		a.internal(w, "create job", err)
	default:
		a.log.Info("job created", "job_id", j.ID)
		go a.logJob(j.ID)
		w.Header().Set("Location", basePath+"/jobs/"+j.ID)
		writeJSON(w, http.StatusCreated, j)
	}
}

func (a *api) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := a.svc.Get(r.PathValue("id"))
	if err != nil {
		a.jobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// getLocalization serves the shared job-text-localization endpoint. It only reads the job.
func (a *api) getLocalization(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("language")
	if strings.TrimSpace(raw) == "" {
		WriteError(w, http.StatusBadRequest, "invalid_request", "language is required", nil)
		return
	}
	lang, ok := localization.Language(raw)
	if !ok {
		WriteError(w, http.StatusBadRequest, "unsupported_language", "language is not supported",
			map[string]any{"supported": localization.Supported})
		return
	}
	j, err := a.svc.Get(r.PathValue("id"))
	if err != nil {
		a.jobError(w, err)
		return
	}
	if !j.Done {
		WriteError(w, http.StatusConflict, "job_not_finished", "job is not finished", nil)
		return
	}
	writeJSON(w, http.StatusOK, a.loc.Localize(j, lang))
}

func (a *api) cancelJob(w http.ResponseWriter, r *http.Request) {
	j, err := a.svc.Cancel(r.PathValue("id"))
	if err != nil {
		a.jobError(w, err)
		return
	}
	a.log.Info("job cancelled", "job_id", j.ID)
	writeJSON(w, http.StatusAccepted, j)
}

func (a *api) jobError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, engine.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found", "job not found", nil)
	case errors.Is(err, engine.ErrJobFinished):
		WriteError(w, http.StatusConflict, "job_finished", "job is already finished", nil)
	default:
		a.internal(w, "job request", err)
	}
}

func (a *api) internal(w http.ResponseWriter, what string, err error) {
	a.log.Error("internal error", "op", what, "err_type", fmt.Sprintf("%T", err)) // message may name endpoints
	WriteError(w, http.StatusInternalServerError, "internal_error", "internal error", nil)
}

// JobSummary is one item of GET /jobs.
type JobSummary struct {
	ID           string          `json:"id"`
	Status       string          `json:"status"`
	Done         bool            `json:"done"`
	CreatedAt    time.Time       `json:"created_at"`
	FinishedAt   *time.Time      `json:"finished_at"`
	Coverage     job.Coverage    `json:"coverage"`
	Finalization summaryFinalize `json:"finalization"`
}

type summaryFinalize struct {
	Status  string  `json:"status"`
	Verdict *string `json:"verdict"`
}

func summarize(j job.Job) JobSummary {
	s := JobSummary{ID: j.ID, Status: j.Status, Done: j.Done, CreatedAt: j.CreatedAt, FinishedAt: j.FinishedAt,
		Coverage: j.Coverage, Finalization: summaryFinalize{Status: j.Finalization.Status}}
	var res struct {
		Verdict string `json:"verdict"`
	}
	if len(j.Finalization.Result) > 0 && json.Unmarshal(j.Finalization.Result, &res) == nil && res.Verdict != "" {
		s.Finalization.Verdict = &res.Verdict
	}
	return s
}

func (a *api) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bad := func(msg string) { WriteError(w, http.StatusBadRequest, "invalid_request", msg, nil) }
	limit := 20
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			bad("limit must be an integer between 1 and 100")
			return
		}
		limit = n
	}
	status := q.Get("status")
	if status != "" && !jobStatuses[status] {
		bad("unknown status")
		return
	}
	var after *store.Cursor
	if c := q.Get("cursor"); c != "" {
		at, id, err := decodeCursor(c)
		if err != nil {
			bad("invalid cursor")
			return
		}
		after = &store.Cursor{CreatedAt: at, ID: id}
	}
	jobs, err := a.svc.Page(status, after, limit+1) // newest first; one more than the page tells whether there is a next
	if err != nil {
		a.internal(w, "list jobs", err)
		return
	}
	var next *string
	if len(jobs) > limit {
		jobs = jobs[:limit]
		next = new(string)
		*next = encodeCursor(jobs[limit-1])
	}
	items := make([]JobSummary, 0, len(jobs))
	for _, j := range jobs {
		items = append(items, summarize(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func encodeCursor(j job.Job) string {
	return base64.RawURLEncoding.EncodeToString([]byte(j.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + j.ID))
}

func decodeCursor(s string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", err
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok || id == "" {
		return time.Time{}, "", errors.New("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	return t, id, err
}

// AnalyzerItem is one item of GET /analyzers. It deliberately has no member
// for execution settings (endpoint, headers, handler): they can carry credentials.
type AnalyzerItem struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Category   string   `json:"category"`
	Enabled    bool     `json:"enabled"`
	DependsOn  []string `json:"depends_on"`
	Trigger    string   `json:"trigger"`
	Selectable bool     `json:"selectable"`
}

func (a *api) listAnalyzers(w http.ResponseWriter, _ *http.Request) {
	items := []AnalyzerItem{}
	for _, d := range a.reg.All() {
		items = append(items, AnalyzerItem{ID: d.ID, Name: d.Name, Category: d.Category, Enabled: d.Enabled,
			DependsOn: append([]string{}, d.DependsOn...), Trigger: d.Trigger, Selectable: d.Trigger != registry.TriggerAfterAll})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// logJob logs analyzer settlements and the job end (ids, statuses and error
// codes only: never results, error messages or URLs).
func (a *api) logJob(id string) {
	sub, err := a.svc.Subscribe(id)
	if err != nil {
		return
	}
	defer sub.Close()
	for ev := range sub.Events() {
		d, _ := ev.Data.(map[string]any)
		switch {
		case ev.Type == "job.snapshot":
			if j, ok := d["job"].(job.Job); ok {
				for _, run := range j.Analyzers {
					if run.Settled() {
						a.logAnalyzer(id, run)
					}
				}
				if j.Done {
					a.log.Info("job finished", "job_id", id, "status", j.Status)
				}
			}
		case strings.HasPrefix(ev.Type, "analyzer.") && ev.Type != "analyzer.started":
			if run, ok := d["analyzer"].(job.AnalyzerRun); ok {
				a.logAnalyzer(id, run)
			}
		case ev.Type == "job.completed" || ev.Type == "job.cancelled" || ev.Type == "job.failed":
			a.log.Info("job finished", "job_id", id, "status", strings.TrimPrefix(ev.Type, "job."))
		}
	}
}

func (a *api) logAnalyzer(jobID string, run job.AnalyzerRun) {
	if run.Reason != nil {
		switch *run.Reason { // creation skips are noise
		case job.ReasonDisabled, job.ReasonNotSelected, job.ReasonNotApplicable:
			return
		}
	}
	attrs := []any{"job_id", jobID, "analyzer_id", run.ID, "status", run.Status}
	if run.Reason != nil {
		attrs = append(attrs, "reason", *run.Reason)
	}
	if run.Error != nil {
		attrs = append(attrs, "error_code", run.Error.Code)
	}
	if run.DurationMS != nil {
		attrs = append(attrs, "duration_ms", *run.DurationMS)
	}
	a.log.Info("analyzer settled", attrs...)
}
