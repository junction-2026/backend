// Package job holds the Job object family shared with the REST and event
// interfaces (analysis-jobs-api). JSON tags match the shared objects.
package job

import (
	"encoding/json"
	"sync"
	"time"
)

// Job statuses.
const (
	StatusQueued     = "queued"
	StatusScraping   = "scraping"
	StatusRunning    = "running"
	StatusFinalizing = "finalizing"
	StatusCompleted  = "completed"
	StatusCancelled  = "cancelled"
	StatusFailed     = "failed"
)

// AnalyzerRun and Finalization statuses.
const (
	RunPending   = "pending"
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunSkipped   = "skipped"
)

// Skip reasons.
const (
	ReasonDisabled          = "disabled"
	ReasonNotSelected       = "not_selected"
	ReasonNotApplicable     = "not_applicable"
	ReasonDependencyFailed  = "dependency_failed"
	ReasonDependencySkipped = "dependency_skipped"
	ReasonCancelled         = "cancelled"
	ReasonNotConfigured     = "not_configured"
	ReasonDisabledByRequest = "disabled_by_request"
	ReasonInputUnavailable  = "input_unavailable"
	ReasonInterrupted       = "interrupted" // pending part when the server stopped (persistent history)
)

// Error codes of failed runs and failed finalization.
const (
	CodeTimeout       = "timeout"
	CodeAdapterError  = "adapter_error"
	CodeInvalidOutput = "invalid_output"
	CodeCancelled     = "cancelled"
	CodeInterrupted   = "interrupted" // running part when the server stopped (persistent history)
)

// RunError is the {"code","message"} error object. It is also the error type
// adapters and finalizers return so the engine can map the code.
type RunError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RunError) Error() string { return e.Code + ": " + e.Message }

// Source is Input.source.
type Source struct {
	URL         string          `json:"url,omitempty"`
	Title       string          `json:"title,omitempty"`
	Author      string          `json:"author,omitempty"`
	Platform    string          `json:"platform,omitempty"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// Input is the analyzed content. NormalizedContent is computed once per job
// and is only exposed to analyzers through the $input binding.
type Input struct {
	Content           string `json:"content"`
	ContentType       string `json:"content_type"`
	Source            Source `json:"source"`
	NormalizedContent string `json:"-"`
}

type Options struct {
	Finalize bool `json:"finalize"`
}

type AnalyzerRun struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Category   string          `json:"category"`
	DependsOn  []string        `json:"depends_on"`
	Status     string          `json:"status"`
	Reason     *string         `json:"reason"`
	Error      *RunError       `json:"error"`
	StartedAt  *time.Time      `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at"`
	DurationMS *int64          `json:"duration_ms"`
	Result     json.RawMessage `json:"result"`
	// Availability and Note are set once by Availability when the run settles.
	Availability *string `json:"availability"`
	Note         *string `json:"note"`
}

// Settled reports succeeded, failed or skipped.
func (r *AnalyzerRun) Settled() bool {
	return r.Status == RunSucceeded || r.Status == RunFailed || r.Status == RunSkipped
}

type Coverage struct {
	Total      int `json:"total"`
	Applicable int `json:"applicable"`
	Pending    int `json:"pending"`
	Running    int `json:"running"`
	Succeeded  int `json:"succeeded"`
	Failed     int `json:"failed"`
	Skipped    int `json:"skipped"`
	Settled    int `json:"settled"`
}

// ComputeCoverage derives Coverage from the runs.
func ComputeCoverage(runs []AnalyzerRun) Coverage {
	c := Coverage{Total: len(runs)}
	for i := range runs {
		r := &runs[i]
		switch r.Status {
		case RunPending:
			c.Pending++
		case RunRunning:
			c.Running++
		case RunSucceeded:
			c.Succeeded++
		case RunFailed:
			c.Failed++
		case RunSkipped:
			c.Skipped++
		}
		if r.Reason == nil || (*r.Reason != ReasonDisabled && *r.Reason != ReasonNotSelected && *r.Reason != ReasonNotApplicable) {
			c.Applicable++
		}
	}
	c.Settled = c.Succeeded + c.Failed + c.Skipped
	return c
}

type Finalization struct {
	Status     string          `json:"status"`
	AnalyzerID *string         `json:"analyzer_id"`
	Reason     *string         `json:"reason"`
	Error      *RunError       `json:"error"`
	StartedAt  *time.Time      `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at"`
	DurationMS *int64          `json:"duration_ms"`
	Result     json.RawMessage `json:"result"` // FinalAssessment
}

type Job struct {
	ID           string        `json:"id"`
	Status       string        `json:"status"`
	Done         bool          `json:"done"`
	CreatedAt    time.Time     `json:"created_at"`
	StartedAt    *time.Time    `json:"started_at"`
	FinishedAt   *time.Time    `json:"finished_at"`
	Input        *Input        `json:"input"`  // null for a scrape job until its scrape succeeded; immutable once set
	Scrape       *Scrape       `json:"scrape"` // null for a job created with input
	Options      Options       `json:"options"`
	Public       bool          `json:"public"` // listed in GET /jobs; fixed at creation
	Analyzers    []AnalyzerRun `json:"analyzers"`
	Coverage     Coverage      `json:"coverage"`
	Finalization Finalization  `json:"finalization"`
}

// UnmarshalJSON reads a Job stored before `public` existed as public.
func (j *Job) UnmarshalJSON(b []byte) error {
	type plain Job
	p := plain{Public: true}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*j = Job(p)
	return nil
}

// Record is a stored Job. Its embedded mutex serializes every state
// transition and read of the job, so REST reads and emitted events agree.
type Record struct {
	sync.Mutex
	Job

	// Event-stream state (guarded by the mutex): the plan, the current Progress, the Activity of each running
	// planned analyzer (and the finalizer) and the preview of each succeeded planned analyzer.
	Plan       *Plan
	Progress   *Progress
	Previews   map[string]any
	Activities map[string]Activity
	LastSeq    int64 // seq of the last event emitted for the job (0 if none)
}

// Snapshot copies the event-stream state. Caller holds the lock.
func (r *Record) Snapshot() Snapshot {
	s := Snapshot{Plan: r.Plan, Previews: map[string]any{}, Activities: map[string]Activity{}}
	if r.Progress != nil {
		p := *r.Progress
		s.Progress = &p
	}
	for k, v := range r.Previews {
		s.Previews[k] = v
	}
	for k, v := range r.Activities {
		s.Activities[k] = v
	}
	return s
}

// Clone returns a copy that is safe to hand to other goroutines. Pointer and
// slice members of runs and of the Scrape are immutable once set, so only the
// run slice and the Scrape struct are copied. Caller holds the lock.
func (j *Job) Clone() Job {
	c := Job{
		ID: j.ID, Status: j.Status, Done: j.Done, CreatedAt: j.CreatedAt,
		StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, Input: j.Input, Options: j.Options, Public: j.Public,
		Analyzers: append([]AnalyzerRun(nil), j.Analyzers...), Coverage: j.Coverage,
		Finalization: j.Finalization,
	}
	if j.Scrape != nil {
		s := *j.Scrape
		c.Scrape = &s
	}
	return c
}

// Scrape error codes produced by the orchestrator; scraper codes pass through.
const (
	CodeScraperUnavailable = "scraper_unavailable"
	CodeNoTextContent      = "no_text_content"
)

// ScrapeRequest is the shared ScrapeRequest object (also the scraper's request body).
type ScrapeRequest struct {
	URL         string `json:"url"`
	MaxPosts    int    `json:"max_posts"`
	MaxComments int    `json:"max_comments"`
}

// ScrapeError is Scrape.error. It is also the error the scraper client returns.
type ScrapeError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
}

func (e *ScrapeError) Error() string { return e.Code + ": " + e.Message }

// Scrape is the shared Scrape object; Status uses the Run* values.
type Scrape struct {
	Status string `json:"status"`
	ScrapeRequest
	Platform   *string           `json:"platform"`
	URLKind    *string           `json:"url_kind"`
	Error      *ScrapeError      `json:"error"`
	Warnings   []json.RawMessage `json:"warnings"` // ScrapeResult warnings verbatim
	StartedAt  *time.Time        `json:"started_at"`
	FinishedAt *time.Time        `json:"finished_at"`
	DurationMS *int64            `json:"duration_ms"`
}

// Str returns a pointer to s.
func Str(s string) *string { return &s }

// FinalAssessment mirrors the shared FinalAssessment object. The finalizer
// runtime owns validation; the engine stores Finalization.Result as raw JSON.
type FinalAssessment struct {
	Verdict    string          `json:"verdict"`
	Confidence *float64        `json:"confidence"`
	UserText   string          `json:"user_text"`
	ProducedBy string          `json:"produced_by"`
	Usage      json.RawMessage `json:"usage"` // provider token usage verbatim; nil marshals as null
	// set by the orchestrator for the llm finalizer only; null otherwise
	Attempts    *int    `json:"attempts"`
	RetryReason *string `json:"retry_reason"`
	// verified by the orchestrator; never nil when built by the finalizer runtime
	WikipediaQuotes []WikipediaQuote `json:"wikipedia_quotes"`
}

// WikipediaQuote is a verbatim part of a WikiLookup passage; everything but Quote is copied from the WikiLookup.
type WikipediaQuote struct {
	Claim      string `json:"claim"`
	Language   string `json:"language"`
	Quote      string `json:"quote"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	RevisionID int64  `json:"revision_id"`
	Coverage   string `json:"coverage"`
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }
