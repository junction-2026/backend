package engine

import (
	"context"
	"errors"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/scraper"
)

// startScrapeLocked marks the scrape running and runs it outside the lock.
func (r *run) startScrapeLocked() {
	s := r.j.Scrape
	now := r.e.now()
	s.Status, s.StartedAt = job.RunRunning, &now
	r.emit("scrape.started", map[string]any{"scrape": *s, "activity": scrapeActivity(s.URL)})
	go r.scrape(s.ScrapeRequest)
}

func (r *run) scrape(req job.ScrapeRequest) {
	defer r.guard()
	ctx, cancel := context.WithTimeout(r.ctx, r.e.cfg.ScrapeTimeout)
	defer cancel()
	res, err := r.e.cfg.Scraper.Scrape(ctx, req)
	r.j.Lock()
	defer r.j.Unlock()
	defer r.recoverJob()
	if r.j.Done || r.cancelled { // cancelled meanwhile: abortLocked settled the scrape
		return
	}
	if err != nil {
		var se *job.ScrapeError
		if !errors.As(err, &se) {
			se = &job.ScrapeError{Code: job.CodeScraperUnavailable, Message: "the scraper is unavailable"}
		}
		r.scrapeFailedLocked(se)
		return
	}
	r.scrapedLocked(res)
}

// endScrapeLocked settles the scrape; rerr nil means succeeded.
func (r *run) endScrapeLocked(rerr *job.ScrapeError) {
	s := r.j.Scrape
	now := r.e.now()
	s.FinishedAt = &now
	if s.StartedAt != nil {
		s.DurationMS = job.Ptr(now.Sub(*s.StartedAt).Milliseconds())
	}
	if rerr != nil {
		s.Status, s.Error = job.RunFailed, rerr
		r.emit("scrape.failed", map[string]any{"scrape": *s})
		return
	}
	s.Status = job.RunSucceeded
	r.emit("scrape.succeeded", map[string]any{"scrape": *s, "input": *r.j.Input})
}

// scrapeFailedLocked: pending runs and finalization skipped (input_unavailable), job failed.
func (r *run) scrapeFailedLocked(rerr *job.ScrapeError) {
	r.endScrapeLocked(rerr)
	reason := job.Str(job.ReasonInputUnavailable)
	for i := range r.j.Analyzers {
		if r.j.Analyzers[i].Status == job.RunPending {
			r.settleLocked(i, job.RunSkipped, reason, nil, nil)
		}
	}
	if f := &r.j.Finalization; f.Status == job.RunPending {
		f.Status, f.Reason = job.RunSkipped, reason
	}
	r.finishLocked(job.StatusFailed)
}

// scrapedLocked sets the mapped input, applies not_applicable and schedules
// the analyzers like a job created with input.
func (r *run) scrapedLocked(res *scraper.Result) {
	s := r.j.Scrape
	s.Platform, s.URLKind, s.Warnings = &res.Platform, &res.URLKind, res.Warnings
	r.j.Input = prepareInput(res.Input)
	r.endScrapeLocked(nil)
	r.j.Status = job.StatusRunning
	r.planLocked() // directly after scrape.succeeded, before the not_applicable skips
	for i := range r.j.Analyzers {
		if r.j.Analyzers[i].Status == job.RunPending && !applies(r.defs[i], *r.j.Input) {
			r.settleLocked(i, job.RunSkipped, job.Str(job.ReasonNotApplicable), nil, nil)
		}
	}
	r.advanceLocked()
}
