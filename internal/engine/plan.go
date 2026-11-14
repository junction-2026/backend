package engine

import (
	"sort"

	"github.com/junction-2026/backend/internal/job"
)

// planLocked computes, stores and emits analysis.planned: the analyzers that are not skipped as disabled or
// not_selected and not hidden, then the finalizer when finalization will run. Never after cancellation.
func (r *run) planLocked() {
	if r.cancelled || r.j.Plan != nil {
		return
	}
	r.planned = map[string]bool{}
	for i := range r.j.Analyzers {
		a := &r.j.Analyzers[i]
		off := a.Status == job.RunSkipped && a.Reason != nil && (*a.Reason == job.ReasonDisabled || *a.Reason == job.ReasonNotSelected)
		if !off && !r.defs[i].HiddenFromProgress() {
			r.planned[a.ID] = true
		}
	}
	phase := map[string]int{}
	var phaseOf func(id string) int
	phaseOf = func(id string) int { // the registry graph is acyclic
		if p, ok := phase[id]; ok {
			return p
		}
		deepest := 0
		for _, d := range r.defs[r.idx[id]].DependsOn {
			if r.planned[d] {
				deepest = max(deepest, phaseOf(d))
			}
		}
		phase[id] = deepest + 1
		return deepest + 1
	}
	entries := []job.PlanEntry{}
	deepest := 0
	for id := range r.planned {
		d := r.defs[r.idx[id]]
		p := phaseOf(id)
		deepest = max(deepest, p)
		entries = append(entries, job.PlanEntry{ID: id, Name: d.Name, ActivityKey: d.ActivityKey(), DependsOn: d.DependsOn, Phase: p})
	}
	sort.Slice(entries, func(a, b int) bool {
		if entries[a].Phase != entries[b].Phase {
			return entries[a].Phase < entries[b].Phase
		}
		return entries[a].ID < entries[b].ID
	})
	if r.finDef != nil {
		entries = append(entries, job.PlanEntry{ID: r.finDef.ID, Name: r.finDef.Name, ActivityKey: keyFinalizer,
			DependsOn: []string{"*"}, Phase: deepest + 1})
	}
	r.j.Plan = &job.Plan{Analyzers: entries, Total: len(entries)}
	r.j.Progress = &job.Progress{Running: []string{}, Total: len(entries)}
	r.j.Previews, r.j.Activities = map[string]any{}, map[string]job.Activity{}
	r.emit("analysis.planned", r.j.Plan)
}

// progressLocked advances done by doneDelta, refreshes running from the run states, stores the new Progress and
// returns it (nil before the plan). Published values are never modified.
func (r *run) progressLocked(doneDelta int) *job.Progress {
	old := r.j.Progress
	if old == nil {
		return nil
	}
	running := []string{} // analyzers are ordered by id
	for i := range r.j.Analyzers {
		if a := &r.j.Analyzers[i]; a.Status == job.RunRunning && r.planned[a.ID] {
			running = append(running, a.ID)
		}
	}
	if r.finDef != nil && r.j.Finalization.Status == job.RunRunning {
		running = append(running, r.finDef.ID)
	}
	r.j.Progress = &job.Progress{Done: old.Done + doneDelta, Running: running, Total: old.Total}
	return r.j.Progress
}

// withProgress adds the progress member to event data (absent before the plan).
func withProgress(data map[string]any, p *job.Progress) map[string]any {
	if p != nil {
		data["progress"] = p
	}
	return data
}
