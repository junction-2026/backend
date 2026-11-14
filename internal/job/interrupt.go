package job

import "time"

const interruptedMessage = "the server stopped before this part finished"

// Interrupt ends a job that was not done when the server stopped (analysis-jobs-api "Jobs interrupted by a
// restart"): unsettled parts become skipped/failed with "interrupted", the job becomes failed, and the event-stream
// state is brought in line (nothing is running, no event is emitted). now is the server start time. A done job is
// left unchanged. The caller owns r exclusively or holds its lock.
func (r *Record) Interrupt(now time.Time) {
	if r.Done {
		return
	}
	end := func(started *time.Time) (finished *time.Time, dur *int64) {
		if started != nil {
			dur = Ptr(max(0, now.Sub(*started).Milliseconds()))
		}
		return &now, dur
	}
	if s := r.Scrape; s != nil && (s.Status == RunPending || s.Status == RunRunning) {
		s.Status, s.Error = RunFailed, &ScrapeError{Code: CodeInterrupted, Message: interruptedMessage}
		s.FinishedAt, s.DurationMS = end(s.StartedAt)
	}
	for i := range r.Analyzers {
		a := &r.Analyzers[i]
		switch a.Status {
		case RunPending:
			a.Status, a.Reason = RunSkipped, Str(ReasonInterrupted)
		case RunRunning:
			a.Status, a.Error = RunFailed, &RunError{Code: CodeInterrupted, Message: interruptedMessage}
		default:
			continue
		}
		a.FinishedAt, a.DurationMS = end(a.StartedAt)
		a.Availability, a.Note = Availability(a, r.Analyzers)
	}
	f := &r.Finalization
	switch f.Status {
	case RunPending:
		f.Status, f.Reason = RunSkipped, Str(ReasonInterrupted)
		f.FinishedAt, f.DurationMS = end(f.StartedAt)
	case RunRunning:
		f.Status, f.Error = RunFailed, &RunError{Code: CodeInterrupted, Message: interruptedMessage}
		f.FinishedAt, f.DurationMS = end(f.StartedAt)
	}
	r.Status, r.Done, r.FinishedAt = StatusFailed, true, &now
	r.Coverage = ComputeCoverage(r.Analyzers)
	r.fixEventState()
}

// fixEventState makes the snapshot state of an ended job consistent: no activity, nothing running, done counted from
// the settled planned parts (same counting as the live progress).
func (r *Record) fixEventState() {
	r.Activities = map[string]Activity{}
	if r.Progress == nil || r.Plan == nil {
		return
	}
	done := 0
	for _, e := range r.Plan.Analyzers {
		settled := false
		if r.Finalization.AnalyzerID != nil && e.ID == *r.Finalization.AnalyzerID {
			settled = r.Finalization.Status != RunPending && r.Finalization.Status != RunSkipped // ended through a settle
		}
		for i := range r.Analyzers {
			if r.Analyzers[i].ID == e.ID {
				settled = r.Analyzers[i].Settled()
			}
		}
		if settled {
			done++
		}
	}
	r.Progress = &Progress{Done: done, Running: []string{}, Total: r.Progress.Total}
}
