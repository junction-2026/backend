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
)

// Error codes of failed runs and failed finalization.
const (
	CodeTimeout       = "timeout"
	CodeAdapterError  = "adapter_error"
	CodeInvalidOutput = "invalid_output"
	CodeCancelled     = "cancelled"
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
	Input        Input         `json:"input"`
	Options      Options       `json:"options"`
	Analyzers    []AnalyzerRun `json:"analyzers"`
	Coverage     Coverage      `json:"coverage"`
	Finalization Finalization  `json:"finalization"`
}

// Record is a stored Job. Its embedded mutex serializes every state
// transition and read of the job, so REST reads and emitted events agree.
type Record struct {
	sync.Mutex
	Job
}

// Clone returns a copy that is safe to hand to other goroutines. Pointer and
// slice members of runs are immutable once set, so only the run slice is
// copied. Caller holds the lock.
func (j *Job) Clone() Job {
	return Job{
		ID: j.ID, Status: j.Status, Done: j.Done, CreatedAt: j.CreatedAt,
		StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, Input: j.Input, Options: j.Options,
		Analyzers: append([]AnalyzerRun(nil), j.Analyzers...), Coverage: j.Coverage,
		Finalization: j.Finalization,
	}
}

// Str returns a pointer to s.
func Str(s string) *string { return &s }

// FinalAssessment mirrors the shared FinalAssessment object. The finalizer
// runtime owns validation and verbatim keeping of extra members; the engine
// stores Finalization.Result as raw JSON.
type FinalAssessment struct {
	Verdict      string          `json:"verdict"`
	OverallScore *float64        `json:"overall_score"`
	Categories   []FinalCategory `json:"categories"`
	Explanation  string          `json:"explanation"`
	Evidence     []EvidenceRef   `json:"evidence"`
	Limitations  []Limitation    `json:"limitations"`
	ProducedBy   string          `json:"produced_by"`
}

type FinalCategory struct {
	ID        string        `json:"id"`
	Score     *float64      `json:"score"`
	Label     string        `json:"label,omitempty"`
	Rationale string        `json:"rationale,omitempty"`
	Evidence  []EvidenceRef `json:"evidence,omitempty"`
}

type EvidenceRef struct {
	AnalyzerID string `json:"analyzer_id"`
	EvidenceID string `json:"evidence_id,omitempty"`
	Note       string `json:"note,omitempty"`
}

type Limitation struct {
	Kind        string   `json:"kind"`
	Message     string   `json:"message"`
	AnalyzerIDs []string `json:"analyzer_ids,omitempty"`
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }
