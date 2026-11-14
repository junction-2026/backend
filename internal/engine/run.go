package engine

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// run is the scheduler state of one job. Everything below the immutable block
// is guarded by j's mutex, which serializes every transition and its events.
type run struct {
	e      *Engine
	j      *job.Record
	ctx    context.Context
	cancel context.CancelFunc
	defs   []*registry.Analyzer // by analyzer index
	idx    map[string]int
	sem    chan struct{}      // per-job analyzer concurrency
	finDef *registry.Analyzer // non-nil when finalization will run

	planned   map[string]bool // analyzers in the plan (not the finalizer); nil before analysis.planned
	launched  []bool
	slot      bool // holds a global job slot
	barrier   bool // ready_for_finalization emitted
	cancelled bool
}

func (r *run) emit(typ string, data any) { r.e.bus.Publish(r.j.ID, typ, data) }

// main starts the job once it owns a global slot (until then it is queued).
func (r *run) main() {
	if g := r.e.gate; g != nil {
		<-g
	}
	r.j.Lock()
	defer r.j.Unlock()
	defer r.recoverJob()
	if r.j.Done { // cancelled while queued: pass the slot on
		r.e.releaseSlot()
		return
	}
	r.slot = true
	r.beginLocked()
	if r.j.Scrape != nil {
		r.startScrapeLocked()
		return
	}
	r.advanceLocked()
}

// guard converts a panic in an execution goroutine (outside any lock) into
// job.failed instead of crashing the process.
func (r *run) guard() {
	if p := recover(); p != nil {
		r.j.Lock()
		defer r.j.Unlock()
		if !r.j.Done {
			r.abortLocked(job.StatusFailed)
		}
	}
}

// recoverJob turns an orchestrator panic into job.failed. Deferred after the
// caller's Unlock registration so the lock is held when it runs.
func (r *run) recoverJob() {
	if p := recover(); p != nil && !r.j.Done {
		r.abortLocked(job.StatusFailed)
	}
}

func (r *run) beginLocked() {
	now := r.e.now()
	r.j.Status, r.j.StartedAt = job.StatusRunning, &now
	if r.j.Scrape != nil {
		r.j.Status = job.StatusScraping
	}
	r.emit("job.started", map[string]any{"job_id": r.j.ID})
	if r.j.Scrape == nil {
		r.planLocked() // directly after job.started, before every analyzer event
	}
	for i := range r.j.Analyzers {
		if a := r.j.Analyzers[i]; a.Status == job.RunSkipped {
			r.emitSkippedLocked(a)
		}
	}
}

// settleLocked moves run i to a settled state (never twice) and emits its event.
func (r *run) settleLocked(i int, status string, reason *string, err *job.RunError, result json.RawMessage) bool {
	a := &r.j.Analyzers[i]
	if a.Settled() {
		return false
	}
	now := r.e.now()
	a.Status, a.Reason, a.Error, a.Result, a.FinishedAt = status, reason, err, result, &now
	a.Availability, a.Note = job.Availability(a, r.j.Analyzers) // deps settled before dependents, so final now
	if a.StartedAt != nil {
		a.DurationMS = job.Ptr(now.Sub(*a.StartedAt).Milliseconds())
	}
	r.j.Coverage = job.ComputeCoverage(r.j.Analyzers)
	delta := 0
	if r.planned[a.ID] {
		delta = 1
		delete(r.j.Activities, a.ID)
	}
	data := map[string]any{"analyzer": *a}
	if status == job.RunSucceeded {
		preview := buildPreview(a.ID, result)
		data["preview"] = preview
		if r.planned[a.ID] && preview != nil {
			r.j.Previews[a.ID] = preview
		}
	} else {
		data["availability"], data["note"] = a.Availability, a.Note
	}
	r.emit("analyzer."+status, withProgress(data, r.progressLocked(delta)))
	return true
}

// emitSkippedLocked emits analyzer.skipped of a run that was skipped at creation (no state change).
func (r *run) emitSkippedLocked(a job.AnalyzerRun) {
	data := map[string]any{"analyzer": a, "availability": a.Availability, "note": a.Note}
	delta := 0
	if r.planned[a.ID] {
		delta = 1
	}
	r.emit("analyzer.skipped", withProgress(data, r.progressLocked(delta)))
}

// advanceLocked resolves every pending analyzer whose dependencies settled
// (skip propagation to a fixpoint, launching the runnable ones), then checks
// the barrier.
func (r *run) advanceLocked() {
	if r.j.Done || r.cancelled {
		return
	}
	for changed := true; changed; {
		changed = false
		for i := range r.j.Analyzers {
			a := &r.j.Analyzers[i]
			if a.Status != job.RunPending || r.launched[i] {
				continue
			}
			ready, failed, skipped := true, false, false
			for _, d := range a.DependsOn {
				dep := &r.j.Analyzers[r.idx[d]]
				if !dep.Settled() {
					ready = false
					break
				}
				failed = failed || dep.Status == job.RunFailed
				skipped = skipped || dep.Status == job.RunSkipped
			}
			switch {
			case !ready:
			case failed:
				changed = r.settleLocked(i, job.RunSkipped, job.Str(job.ReasonDependencyFailed), nil, nil)
			case skipped:
				changed = r.settleLocked(i, job.RunSkipped, job.Str(job.ReasonDependencySkipped), nil, nil)
			default:
				r.launched[i] = true
				go r.exec(i, adapter.Inputs(r.defs[i], &r.j.Job))
			}
		}
	}
	r.barrierLocked()
}

// barrierLocked fires exactly once, after the settle event of the last run.
func (r *run) barrierLocked() {
	if r.barrier || r.j.Done || r.cancelled || r.j.Coverage.Settled != r.j.Coverage.Total {
		return
	}
	r.barrier = true
	r.j.Status = job.StatusFinalizing
	r.emit("analysis.ready_for_finalization", map[string]any{"coverage": r.j.Coverage})
	if r.finDef == nil { // finalization skipped at creation
		r.finishLocked(job.StatusCompleted)
		return
	}
	now := r.e.now()
	f := &r.j.Finalization
	f.Status, f.StartedAt = job.RunRunning, &now
	act := newActivity(keyFinalizer, r.finDef.Name)
	r.j.Activities[r.finDef.ID] = act
	r.emit("analysis.finalizing", withProgress(map[string]any{"finalization": *f, "activity": act}, r.progressLocked(0)))
	go r.finalize(FinalizationInput{Job: r.j.Clone(), Analyzer: r.finDef})
}

// exec runs analyzer i once a per-job slot is free.
func (r *run) exec(i int, inputs map[string]any) {
	defer r.guard()
	select {
	case r.sem <- struct{}{}:
	case <-r.ctx.Done():
		return
	}
	defer func() { <-r.sem }()
	if !r.startRun(i, inputs) {
		return
	}
	def := r.defs[i]
	ctx, cancel := context.WithTimeout(r.ctx, def.Execution.Timeout)
	defer cancel()
	raw, err := r.e.cfg.Executor.Execute(ctx, def, adapter.Request{JobID: r.j.ID, AnalyzerID: def.ID, Inputs: inputs})
	var skip *adapter.Skip
	if errors.As(err, &skip) { // the adapter decided at run time that there is nothing to do
		r.finishRun(i, nil, nil, &skip.Reason)
		return
	}
	rerr := classify(ctx, err)
	if rerr == nil {
		if verr := def.ValidateOutput(raw); verr != nil {
			rerr = &job.RunError{Code: job.CodeInvalidOutput, Message: verr.Error()}
		}
	}
	r.finishRun(i, raw, rerr, nil)
}

func (r *run) startRun(i int, inputs map[string]any) bool {
	r.j.Lock()
	defer r.j.Unlock()
	defer r.recoverJob()
	a := &r.j.Analyzers[i]
	if r.j.Done || a.Status != job.RunPending {
		return false
	}
	now := r.e.now()
	a.Status, a.StartedAt = job.RunRunning, &now
	r.j.Coverage = job.ComputeCoverage(r.j.Analyzers)
	act := activityFor(r.defs[i], inputs)
	if r.planned[a.ID] {
		r.j.Activities[a.ID] = act
	}
	r.emit("analyzer.started", withProgress(map[string]any{"analyzer": *a, "activity": act}, r.progressLocked(0)))
	return true
}

func (r *run) finishRun(i int, raw json.RawMessage, rerr *job.RunError, skipReason *string) {
	r.j.Lock()
	defer r.j.Unlock()
	defer r.recoverJob()
	if r.j.Done || r.j.Analyzers[i].Status != job.RunRunning { // cancelled meanwhile
		return
	}
	switch {
	case skipReason != nil:
		r.settleLocked(i, job.RunSkipped, skipReason, nil, nil)
	case rerr != nil:
		r.settleLocked(i, job.RunFailed, nil, rerr, nil)
	default:
		r.settleLocked(i, job.RunSucceeded, nil, nil, raw)
	}
	r.advanceLocked()
}

// classify maps an execution error to a RunError (nil on success).
func classify(ctx context.Context, err error) *job.RunError {
	if err == nil {
		return nil
	}
	var re *job.RunError
	if errors.As(err, &re) {
		return re
	}
	if e := adapter.ContextError(ctx); e != nil {
		return e
	}
	return &job.RunError{Code: job.CodeAdapterError, Message: err.Error()}
}

func (r *run) finalize(in FinalizationInput) {
	defer r.guard()
	ctx, cancel := context.WithTimeout(r.ctx, r.finDef.Execution.Timeout)
	defer cancel()
	type res struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan res, 1) // buffered: a finalizer that ignores ctx must not leak blocked
	go func() {
		defer r.guard()
		raw, err := r.e.cfg.Finalizer.Run(ctx, in)
		ch <- res{raw, err}
	}()
	var raw json.RawMessage
	var err error
	select {
	case v := <-ch:
		raw, err = v.raw, v.err
	case <-ctx.Done(): // timeout or cancel bounds a finalizer that ignores its ctx; its late result is dropped
		err = ctx.Err()
	}
	rerr := classify(ctx, err)
	if rerr == nil && (len(raw) == 0 || raw[0] != '{' || !json.Valid(raw)) {
		rerr = &job.RunError{Code: job.CodeInvalidOutput, Message: "finalizer result is not a JSON object"}
	}
	r.j.Lock()
	defer r.j.Unlock()
	defer r.recoverJob()
	f := &r.j.Finalization
	if r.j.Done || f.Status != job.RunRunning {
		return
	}
	r.endFinalizationLocked(raw, rerr)
	r.finishLocked(job.StatusCompleted)
}

// endFinalizationLocked settles a running finalization and emits its event.
func (r *run) endFinalizationLocked(raw json.RawMessage, rerr *job.RunError) {
	f := &r.j.Finalization
	now := r.e.now()
	f.FinishedAt, f.DurationMS = &now, job.Ptr(now.Sub(*f.StartedAt).Milliseconds())
	typ := "analysis.finalized"
	if rerr != nil {
		f.Status, f.Error, typ = job.RunFailed, rerr, "analysis.finalization_failed"
	} else {
		f.Status, f.Result = job.StatusCompleted, raw
	}
	delete(r.j.Activities, r.finDef.ID)
	r.emit(typ, withProgress(map[string]any{"finalization": *f}, r.progressLocked(1)))
}

// abortLocked ends the job as cancelled (or failed on an internal error):
// settled runs are kept, pending runs are skipped, running ones failed with
// cancelled, then the finalization, then the terminal event.
func (r *run) abortLocked(status string) {
	r.cancelled = true
	r.cancel()
	if s := r.j.Scrape; s != nil && (s.Status == job.RunPending || s.Status == job.RunRunning) {
		r.endScrapeLocked(&job.ScrapeError{Code: job.CodeCancelled, Message: "scrape aborted: job " + status})
	}
	cancelled := &job.RunError{Code: job.CodeCancelled, Message: "analyzer aborted: job " + status}
	for i := range r.j.Analyzers {
		switch r.j.Analyzers[i].Status {
		case job.RunPending:
			r.settleLocked(i, job.RunSkipped, job.Str(job.ReasonCancelled), nil, nil)
		case job.RunRunning:
			r.settleLocked(i, job.RunFailed, nil, cancelled, nil)
		}
	}
	switch f := &r.j.Finalization; f.Status {
	case job.RunRunning:
		r.endFinalizationLocked(nil, &job.RunError{Code: job.CodeCancelled, Message: "finalization aborted: job " + status})
	case job.RunPending:
		f.Status, f.Reason = job.RunSkipped, job.Str(job.ReasonCancelled)
	}
	r.finishLocked(status)
}

// finishLocked emits the terminal event and releases the job's resources.
func (r *run) finishLocked(status string) {
	now := r.e.now()
	r.j.Status, r.j.Done, r.j.FinishedAt = status, true, &now
	r.j.Coverage = job.ComputeCoverage(r.j.Analyzers)
	r.emit("job."+status, map[string]any{"job": r.j.Clone()})
	r.e.bus.Close(r.j.ID)
	r.cancel()
	if r.slot {
		r.slot = false
		r.e.releaseSlot()
	}
}
