// Package engine runs analysis jobs: DAG scheduling, concurrency limits,
// cancellation, the settle barrier and the finalizer hook.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
	"github.com/junction-2026/backend/internal/scraper"
	"github.com/junction-2026/backend/internal/store"
)

var (
	ErrNotFound    = errors.New("job not found")
	ErrJobFinished = errors.New("job already finished")
	// ErrScrapeUnavailable: Create with Scrape while no scraper is configured.
	ErrScrapeUnavailable = errors.New("no scraper configured")
)

// UnknownAnalyzerError: Create named ids that are not in the registry.
type UnknownAnalyzerError struct{ IDs []string }

func (e *UnknownAnalyzerError) Error() string {
	return "unknown analyzers: " + strings.Join(e.IDs, ", ")
}

// NotSelectableError: Create named after_all_analyzers_settled analyzers.
type NotSelectableError struct{ IDs []string }

func (e *NotSelectableError) Error() string {
	return "analyzers not selectable: " + strings.Join(e.IDs, ", ")
}

// FinalizationInput is what the Finalizer hook receives: a consistent clone
// of the job (all analyzers settled) and the finalizer's registry definition.
type FinalizationInput struct {
	Job      job.Job
	Analyzer *registry.Analyzer
}

// Finalizer is the synthesis hook, invoked at most once per job after
// analysis.ready_for_finalization. It returns the FinalAssessment as JSON; a
// returned *job.RunError carries its own code, other errors are adapter_error.
// The context carries the finalizer's configured timeout and job cancellation.
type Finalizer interface {
	Run(ctx context.Context, in FinalizationInput) (json.RawMessage, error)
}

// Executor runs one analyzer; adapter.Executor implements it.
type Executor interface {
	Execute(ctx context.Context, def *registry.Analyzer, req adapter.Request) (json.RawMessage, error)
}

type Config struct {
	MaxParallelAnalyzers int             // per job, default 8
	MaxParallelJobs      int             // global, default 16; further jobs stay queued
	JobRetention         time.Duration   // default 24h
	SweepInterval        time.Duration   // eviction period, default 1m
	EventBuffer          int             // per subscriber, default 256
	Finalizer            Finalizer       // nil: finalization is skipped / not_configured
	Executor             Executor        // default: adapter.Executor with built-in handlers
	Store                store.Store     // default: in memory
	Scraper              *scraper.Client // nil: jobs with Scrape are refused (ErrScrapeUnavailable)
	ScrapeTimeout        time.Duration   // default 360s: an unrendered page fetch, a rendered one (>= 150 s) and one 429/5xx retry
	Now                  func() time.Time
}

// ConfigFromEnv reads MAX_PARALLEL_ANALYZERS, MAX_PARALLEL_JOBS and JOB_RETENTION.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	var c Config
	for name, dst := range map[string]*int{"MAX_PARALLEL_ANALYZERS": &c.MaxParallelAnalyzers, "MAX_PARALLEL_JOBS": &c.MaxParallelJobs} {
		if v := getenv(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return c, fmt.Errorf("%s must be a positive integer", name)
			}
			*dst = n
		}
	}
	if v := getenv("JOB_RETENTION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return c, fmt.Errorf("JOB_RETENTION must be a positive duration")
		}
		c.JobRetention = d
	}
	return c, nil
}

// CreateRequest mirrors POST /jobs. Analyzers nil selects all; non-nil (even
// empty) selects only those plus their dependencies. Finalize nil means true.
// Input validation (blank content, content types) is the HTTP layer's job.
// A non-nil Scrape (defaults applied by the caller) replaces Input.
type CreateRequest struct {
	Input     job.Input
	Scrape    *job.ScrapeRequest
	Analyzers []string
	Finalize  *bool
}

type Engine struct {
	reg   *registry.Registry
	cfg   Config
	bus   *events.Bus
	store store.Store
	ctx   context.Context
	stop  context.CancelFunc

	mu      sync.Mutex
	runs    map[string]*run
	running int    // jobs holding a global slot
	waiting []*run // queued jobs in creation order

	gate chan struct{} // test seam: jobs wait for it to be closed before starting
}

func New(reg *registry.Registry, cfg Config) *Engine {
	if cfg.MaxParallelAnalyzers < 1 {
		cfg.MaxParallelAnalyzers = 8
	}
	if cfg.MaxParallelJobs < 1 {
		cfg.MaxParallelJobs = 16
	}
	if cfg.JobRetention <= 0 {
		cfg.JobRetention = 24 * time.Hour
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = time.Minute
	}
	if cfg.EventBuffer <= 0 {
		cfg.EventBuffer = 256
	}
	if cfg.Executor == nil {
		cfg.Executor = &adapter.Executor{Handlers: adapter.NewHandlers()}
	}
	if cfg.Store == nil {
		cfg.Store = store.NewMemory()
	}
	if cfg.ScrapeTimeout <= 0 {
		cfg.ScrapeTimeout = 360 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	e := &Engine{reg: reg, cfg: cfg, bus: events.New(cfg.EventBuffer), store: cfg.Store, runs: map[string]*run{}}
	e.ctx, e.stop = context.WithCancel(context.Background())
	go func() {
		t := time.NewTicker(cfg.SweepInterval)
		defer t.Stop()
		for {
			select {
			case <-e.ctx.Done():
				return
			case <-t.C:
				e.Sweep(e.cfg.Now())
			}
		}
	}()
	return e
}

// Close stops the sweeper and aborts every running analyzer. Jobs are not
// finished; the process is shutting down.
func (e *Engine) Close() { e.stop() }

// Sweep evicts jobs finished more than JobRetention before now.
func (e *Engine) Sweep(now time.Time) {
	ids := e.store.EvictFinished(now.Add(-e.cfg.JobRetention))
	e.mu.Lock()
	for _, id := range ids {
		delete(e.runs, id)
	}
	e.mu.Unlock()
	for _, id := range ids {
		e.bus.Remove(id)
	}
}

func (e *Engine) now() time.Time { return e.cfg.Now().UTC() }

// Create stores a new job (status queued), starts it asynchronously and
// returns its initial state.
func (e *Engine) Create(req CreateRequest) (job.Job, error) {
	if req.Scrape != nil && e.cfg.Scraper == nil {
		return job.Job{}, ErrScrapeUnavailable
	}
	selected, err := e.selection(req.Analyzers)
	if err != nil {
		return job.Job{}, err
	}
	var in *job.Input
	var scrape *job.Scrape
	if req.Scrape != nil {
		scrape = &job.Scrape{Status: job.RunPending, ScrapeRequest: *req.Scrape, Warnings: []json.RawMessage{}}
	} else {
		in = prepareInput(req.Input)
	}
	finalize := req.Finalize == nil || *req.Finalize

	j := &job.Record{Job: job.Job{ID: uuid.NewString(), Status: job.StatusQueued, CreatedAt: e.now(), Input: in,
		Scrape: scrape, Options: job.Options{Finalize: finalize}, Analyzers: []job.AnalyzerRun{},
		Finalization: job.Finalization{Status: job.RunPending}}}
	r := &run{e: e, j: j, idx: map[string]int{}, sem: make(chan struct{}, e.cfg.MaxParallelAnalyzers)}
	r.ctx, r.cancel = context.WithCancel(e.ctx)
	for _, a := range e.reg.All() {
		if a.Trigger == registry.TriggerAfterAll {
			continue
		}
		run := job.AnalyzerRun{ID: a.ID, Name: a.Name, Category: a.Category, DependsOn: a.DependsOn, Status: job.RunPending}
		switch {
		case !a.Enabled:
			run.Status, run.Reason = job.RunSkipped, job.Str(job.ReasonDisabled)
		case !selected[a.ID]:
			run.Status, run.Reason = job.RunSkipped, job.Str(job.ReasonNotSelected)
		case in != nil && !applies(a, *in): // scrape jobs: after input mapping (run.scraped)
			run.Status, run.Reason = job.RunSkipped, job.Str(job.ReasonNotApplicable)
		}
		run.Availability, run.Note = job.Availability(&run, nil) // creation-time skips never depend on other runs
		r.idx[a.ID] = len(j.Analyzers)
		r.defs = append(r.defs, a)
		j.Analyzers = append(j.Analyzers, run)
	}
	r.launched = make([]bool, len(j.Analyzers))
	j.Coverage = job.ComputeCoverage(j.Analyzers)
	fd := e.reg.Finalizer()
	if fd != nil {
		j.Finalization.AnalyzerID = job.Str(fd.ID)
	}
	switch {
	case !finalize:
		j.Finalization.Status, j.Finalization.Reason = job.RunSkipped, job.Str(job.ReasonDisabledByRequest)
	case fd == nil || e.cfg.Finalizer == nil:
		j.Finalization.Status, j.Finalization.Reason = job.RunSkipped, job.Str(job.ReasonNotConfigured)
	default:
		r.finDef = fd
	}

	e.store.Put(j)
	j.Lock()
	snap := j.Clone()
	j.Unlock()
	e.mu.Lock()
	e.runs[j.ID] = r
	if e.running < e.cfg.MaxParallelJobs {
		e.running++
		go r.main()
	} else {
		e.waiting = append(e.waiting, r)
	}
	e.mu.Unlock()
	return snap, nil
}

// prepareInput applies Input defaults and computes normalized_content.
func prepareInput(in job.Input) *job.Input {
	if in.ContentType == "" {
		in.ContentType = "text/plain"
	}
	if in.Source.PublishedAt != nil {
		t := in.Source.PublishedAt.UTC()
		in.Source.PublishedAt = &t
	}
	in.NormalizedContent = job.Normalize(in.Content, in.ContentType)
	return &in
}

// selection returns the selected analyzer ids (with dependencies).
func (e *Engine) selection(ids []string) (map[string]bool, error) {
	if ids == nil {
		all := map[string]bool{}
		for _, a := range e.reg.All() {
			all[a.ID] = true
		}
		return all, nil
	}
	var unknown, final []string
	for _, id := range ids {
		switch a := e.reg.Get(id); {
		case a == nil:
			unknown = append(unknown, id)
		case a.Trigger == registry.TriggerAfterAll:
			final = append(final, id)
		}
	}
	if len(unknown) > 0 {
		return nil, &UnknownAnalyzerError{IDs: dedup(unknown)}
	}
	if len(final) > 0 {
		return nil, &NotSelectableError{IDs: dedup(final)}
	}
	return e.reg.Closure(ids), nil
}

func dedup(ids []string) []string {
	sort.Strings(ids)
	out := ids[:0]
	for i, id := range ids {
		if i == 0 || id != ids[i-1] {
			out = append(out, id)
		}
	}
	return out
}

// applies evaluates applies_to against the input (once, at creation or after the scrape).
func applies(a *registry.Analyzer, in job.Input) bool {
	at := a.AppliesTo
	if len(at.ContentTypes) > 0 {
		ct := strings.ToLower(strings.TrimSpace(strings.SplitN(in.ContentType, ";", 2)[0]))
		ok := false
		for _, t := range at.ContentTypes {
			ok = ok || strings.ToLower(t) == ct
		}
		if !ok {
			return false
		}
	}
	if at.RequiresSourceURL && in.Source.URL == "" {
		return false
	}
	return utf8.RuneCountInString(in.NormalizedContent) >= at.MinContentLength
}

// releaseSlot hands a global slot to the longest-waiting job, or frees it.
func (e *Engine) releaseSlot() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.waiting) > 0 {
		next := e.waiting[0]
		e.waiting = e.waiting[1:]
		go next.main()
		return
	}
	e.running--
}

func (e *Engine) lookup(id string) (*run, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r := e.runs[id]; r != nil {
		return r, nil
	}
	return nil, ErrNotFound
}

// Get returns a consistent copy of the job.
func (e *Engine) Get(id string) (job.Job, error) {
	j, ok := e.store.Get(id)
	if !ok {
		return job.Job{}, ErrNotFound
	}
	j.Lock()
	defer j.Unlock()
	return j.Clone(), nil
}

// List returns jobs newest first (created_at, ties by id), optionally only
// those with the given status.
func (e *Engine) List(status string) []job.Job {
	var out []job.Job
	for _, j := range e.store.All() {
		j.Lock()
		if status == "" || j.Status == status {
			out = append(out, j.Clone())
		}
		j.Unlock()
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].CreatedAt.Equal(out[b].CreatedAt) {
			return out[a].CreatedAt.After(out[b].CreatedAt)
		}
		return out[a].ID < out[b].ID
	})
	return out
}

// Subscribe returns a snapshot (seq = last emitted) followed by every later
// event of the job. For a finished job only the snapshot is delivered.
func (e *Engine) Subscribe(id string) (*events.Subscription, error) {
	r, err := e.lookup(id)
	if err != nil {
		return nil, err
	}
	r.j.Lock()
	defer r.j.Unlock()
	return e.bus.Subscribe(id, func() any { return map[string]any{"job": r.j.Clone()} }), nil
}

// Cancel cancels a job and returns its final state (status cancelled).
func (e *Engine) Cancel(id string) (job.Job, error) {
	r, err := e.lookup(id)
	if err != nil {
		return job.Job{}, err
	}
	r.j.Lock()
	defer r.j.Unlock()
	if r.j.Done {
		return job.Job{}, ErrJobFinished
	}
	if r.j.Status == job.StatusQueued {
		r.beginLocked() // leaves queued: job.started and creation-skip events precede the cancellation
	}
	r.abortLocked(job.StatusCancelled)
	return r.j.Clone(), nil
}
