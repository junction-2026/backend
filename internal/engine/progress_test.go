package engine

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/job"
	ss "github.com/junction-2026/backend/internal/socialscraper"
)

func data(ev events.Event) map[string]any { m, _ := ev.Data.(map[string]any); return m }

func progressOf(ev events.Event) *job.Progress {
	p, _ := data(ev)["progress"].(*job.Progress)
	return p
}

func planOf(evs []events.Event) *job.Plan {
	for _, ev := range evs {
		if ev.Type == "analysis.planned" {
			return ev.Data.(*job.Plan)
		}
	}
	return nil
}

func planIDs(p *job.Plan) (ids []string) {
	for _, e := range p.Analyzers {
		ids = append(ids, e.ID)
	}
	return
}

// checkProgress verifies the progress rules on a whole stream: no progress on settle events before the plan,
// done +1 per settle event of a planned analyzer, done total-1 on finalizing and total on finalized.
func checkProgress(t *testing.T, evs []events.Event) {
	t.Helper()
	var plan *job.Plan
	planned := map[string]bool{}
	done := 0
	for _, ev := range evs {
		p := progressOf(ev)
		switch ev.Type {
		case "analysis.planned":
			require.Nil(t, plan, "planned once")
			plan = ev.Data.(*job.Plan)
			require.Equal(t, len(plan.Analyzers), plan.Total)
			for _, e := range plan.Analyzers {
				planned[e.ID] = len(e.DependsOn) != 1 || e.DependsOn[0] != "*"
			}
		case "analyzer.started", "analyzer.succeeded", "analyzer.failed", "analyzer.skipped":
			id := data(ev)["analyzer"].(job.AnalyzerRun).ID
			if plan == nil {
				require.Nil(t, p, "%s before the plan has no progress", ev.Type)
				continue
			}
			require.NotNil(t, p, "%s:%s", ev.Type, id)
			if ev.Type != "analyzer.started" && planned[id] {
				done++
			}
			require.Equal(t, done, p.Done, "%s:%s", ev.Type, id)
			require.Equal(t, plan.Total, p.Total)
			require.True(t, sort.StringsAreSorted(p.Running), "running ordered by id")
			require.NotNil(t, p.Running)
		case "analysis.finalizing":
			require.Equal(t, job.Progress{Done: plan.Total - 1, Running: []string{plan.Analyzers[plan.Total-1].ID}, Total: plan.Total}, *p)
			require.Equal(t, "finalizer", data(ev)["activity"].(job.Activity).Key)
		case "analysis.finalized", "analysis.finalization_failed":
			require.Equal(t, job.Progress{Done: plan.Total, Running: []string{}, Total: plan.Total}, *p)
		}
	}
}

func TestPlanTextJob(t *testing.T) {
	f := &fakeFinalizer{}
	e := newEngine(t, regYAML(an("a", "ok"), an("b", "ok", deps("a")), an("c", "ok", deps("a")), an("d", "ok", deps("b", "c")),
		an("text_stats", "ok"), finDef), Config{Finalizer: f}, map[string]adapter.Handler{"ok": ok, "final": ok})
	_, sub := createWithEvents(t, e, in("hello"))
	evs := drain(t, sub)
	requireSeq(t, evs)
	require.Equal(t, []string{"job.snapshot", "job.started", "analysis.planned"}, types(evs)[:3])

	plan := planOf(evs)
	require.Equal(t, 5, plan.Total)
	require.Equal(t, []job.PlanEntry{
		{ID: "a", Name: "a", ActivityKey: "a", DependsOn: []string{}, Phase: 1},
		{ID: "b", Name: "b", ActivityKey: "b", DependsOn: []string{"a"}, Phase: 2},
		{ID: "c", Name: "c", ActivityKey: "c", DependsOn: []string{"a"}, Phase: 2},
		{ID: "d", Name: "d", ActivityKey: "d", DependsOn: []string{"b", "c"}, Phase: 3},
		{ID: "final_synthesis", Name: "final_synthesis", ActivityKey: "finalizer", DependsOn: []string{"*"}, Phase: 4},
	}, plan.Analyzers, "text_stats is hidden; the finalizer is last")
	checkProgress(t, evs)

	var dones []int
	for _, ev := range evs {
		if ev.Type == "analyzer.succeeded" && data(ev)["analyzer"].(job.AnalyzerRun).ID != "text_stats" {
			dones = append(dones, progressOf(ev).Done)
		}
	}
	require.Equal(t, []int{1, 2, 3, 4}, dones)
	// hidden text_stats still runs and has events; its progress is unchanged by it
	require.Equal(t, 1, count(evs, "analyzer.started")-4)
	for _, ev := range evs {
		if ev.Type == "analyzer.succeeded" {
			require.Contains(t, data(ev), "preview")
			require.Nil(t, data(ev)["preview"], "no preview shape for these analyzers")
		}
	}
}

func TestPlanSelectionAndFinalizeFalse(t *testing.T) {
	reg := regYAML(an("a", "ok"), an("b", "ok", deps("a")), an("c", "ok"), finDef)
	h := map[string]adapter.Handler{"ok": ok, "final": ok}
	e := newEngine(t, reg, Config{Finalizer: &fakeFinalizer{}}, h)

	_, sub := createWithEvents(t, e, CreateRequest{Input: job.Input{Content: "x"}, Analyzers: []string{"b"}})
	evs := drain(t, sub)
	require.Equal(t, []string{"a", "b", "final_synthesis"}, planIDs(planOf(evs)))
	checkProgress(t, evs)
	ts := typesFor(evs)
	require.Less(t, index(ts, "analysis.planned"), index(ts, "analyzer.skipped:c"))
	for _, ev := range evs {
		if ev.Type == "analyzer.skipped" { // not_selected: unplanned, progress unchanged
			require.Equal(t, 0, progressOf(ev).Done)
			require.Equal(t, "not_applicable", *data(ev)["availability"].(*string))
		}
	}

	no := false
	_, sub = createWithEvents(t, e, CreateRequest{Input: job.Input{Content: "x"}, Finalize: &no})
	evs = drain(t, sub)
	require.Equal(t, []string{"a", "c", "b"}, planIDs(planOf(evs)), "by phase, then id")
	checkProgress(t, evs)
	var lastSettle *job.Progress
	for _, ev := range evs {
		if ev.Type == "analyzer.succeeded" {
			lastSettle = progressOf(ev)
		}
	}
	require.Equal(t, 3, lastSettle.Done, "without a finalizer entry done reaches total at the barrier")
	require.Equal(t, 3, lastSettle.Total)

	// a registry without a finalizer
	e2 := newEngine(t, regYAML(an("a", "ok"), an("b", "ok", deps("a"))), Config{}, h)
	_, sub = createWithEvents(t, e2, in("x"))
	evs = drain(t, sub)
	require.Equal(t, []string{"a", "b"}, planIDs(planOf(evs)))
	checkProgress(t, evs)
	// finalizer present in the registry but not configured
	e3 := newEngine(t, regYAML(an("a", "ok"), finDef), Config{}, h)
	_, sub = createWithEvents(t, e3, in("x"))
	require.Equal(t, []string{"a"}, planIDs(planOf(drain(t, sub))))
}

func TestPlanNotApplicableFailedAndDependencySkipped(t *testing.T) {
	boom := func(context.Context, adapter.Request) (any, error) {
		return nil, &job.RunError{Code: job.CodeAdapterError, Message: "boom"}
	}
	reg := regYAML(an("a", "boom"), an("b", "ok", deps("a")), an("c", "ok"), an("long", "ok", "    applies_to: {min_content_length: 1000}\n"), finDef)
	e := newEngine(t, reg, Config{Finalizer: &fakeFinalizer{}}, map[string]adapter.Handler{"ok": ok, "boom": boom, "final": ok})
	j, sub := createWithEvents(t, e, in("short"))
	evs := drain(t, sub)
	checkProgress(t, evs)
	require.Equal(t, []string{"a", "c", "long", "b", "final_synthesis"}, planIDs(planOf(evs)), "not_applicable stays planned")
	fin := waitDone(t, e, j.ID)
	require.Equal(t, job.ReasonDependencyFailed, *ar(fin, "b").Reason)
	ts := typesFor(evs)
	require.Less(t, index(ts, "analysis.planned"), index(ts, "analyzer.skipped:long"))
	for _, ev := range evs {
		d := data(ev)
		switch ev.Type {
		case "analyzer.failed", "analyzer.skipped":
			run := d["analyzer"].(job.AnalyzerRun)
			require.Equal(t, run.Availability, d["availability"])
			require.Equal(t, run.Note, d["note"])
		}
	}
}

func TestHiddenByRegistry(t *testing.T) {
	reg := regYAML(an("a", "ok"), an("h", "ok", "    ui: {hidden: true}\n"), an("text_stats", "ok", "    ui: {hidden: false}\n"))
	e := newEngine(t, reg, Config{}, map[string]adapter.Handler{"ok": ok})
	_, sub := createWithEvents(t, e, in("x"))
	evs := drain(t, sub)
	require.Equal(t, []string{"a", "text_stats"}, planIDs(planOf(evs)))
	checkProgress(t, evs)
}

func TestCancelledMidRun(t *testing.T) {
	started := make(chan struct{})
	h := map[string]adapter.Handler{"ok": ok, "final": ok, "block": func(ctx context.Context, r adapter.Request) (any, error) {
		close(started)
		return block(ctx, r)
	}}
	e := newEngine(t, regYAML(an("a", "ok"), an("b", "block", deps("a")), an("c", "ok", deps("b")), finDef), Config{Finalizer: &fakeFinalizer{}}, h)
	j, sub := createWithEvents(t, e, in("x"))
	<-started
	_, err := e.Cancel(j.ID)
	require.NoError(t, err)
	evs := drain(t, sub)
	checkProgress(t, evs)
	require.Equal(t, 4, planOf(evs).Total)
	var last *job.Progress
	for _, ev := range evs {
		if p := progressOf(ev); p != nil {
			last = p
		}
	}
	require.Equal(t, job.Progress{Done: 3, Running: []string{}, Total: 4}, *last, "a, b (cancelled), c (skipped) settled; finalization never ran")

	// cancelled while queued: no plan
	e2 := newEngine(t, regYAML(an("a", "block")), Config{MaxParallelJobs: 1}, map[string]adapter.Handler{"block": block})
	first, _ := e2.Create(in("x"))
	q, _ := e2.Create(in("y"))
	sub2, err := e2.Subscribe(q.ID)
	require.NoError(t, err)
	_, err = e2.Cancel(q.ID)
	require.NoError(t, err)
	evs = drain(t, sub2)
	require.Nil(t, planOf(evs))
	checkProgress(t, evs)
	_, _ = e2.Cancel(first.ID)
}

func TestScrapePlanPosition(t *testing.T) {
	sc := stubScraper(reply(postResult(), nil))
	e := newEngine(t, scrapeRegistry, Config{Scraper: sc, Finalizer: &fakeFinalizer{}}, map[string]adapter.Handler{"ok": ok, "final": ok})
	_, sub := createWithEvents(t, e, scrapeReq("https://www.Mastodon.social/@alice/1"))
	evs := drain(t, sub)
	checkProgress(t, evs)
	ts := typesFor(evs)
	require.Equal(t, index(ts, "scrape.succeeded")+1, index(ts, "analysis.planned"))
	require.Equal(t, []string{"a", "long", "final_synthesis"}, planIDs(planOf(evs)))
	started := data(evs[index(ts, "scrape.started")])
	act := started["activity"].(job.Activity)
	require.Equal(t, "scrape", act.Key)
	require.Equal(t, "Fetching the content", act.Text)
	require.Equal(t, "from mastodon.social", *act.Detail)
	require.Equal(t, map[string]any{"host": "mastodon.social"}, act.Params)
	// the disabled skip before the plan has no progress; not_applicable after it counts
	for _, ev := range evs {
		if ev.Type == "analyzer.skipped" {
			id := data(ev)["analyzer"].(job.AnalyzerRun).ID
			if id == "off" {
				require.Nil(t, progressOf(ev))
			} else {
				require.Equal(t, 1, progressOf(ev).Done)
			}
		}
	}

	// failed scrape: no plan
	e = newEngine(t, scrapeRegistry, Config{Scraper: stubScraper(reply(nil, ss.AccountPrivate("mastodon", "private")))}, map[string]adapter.Handler{"ok": ok, "final": ok})
	_, sub = createWithEvents(t, e, scrapeReq("https://mastodon.social/@alice"))
	evs = drain(t, sub)
	require.Nil(t, planOf(evs))
	checkProgress(t, evs)
}

func TestSnapshotCarriesProgressState(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	h := map[string]adapter.Handler{"final": ok,
		"tr": func(context.Context, adapter.Request) (any, error) {
			return map[string]any{"source_language": "de", "translated_text": "Hallo"}, nil
		},
		"wait": func(ctx context.Context, _ adapter.Request) (any, error) {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return map[string]any{}, nil
		}}
	e := newEngine(t, regYAML(an("translation", "tr"), an("news_search", "wait", deps("translation")), finDef), Config{Finalizer: &fakeFinalizer{}}, h)
	j, sub := createWithEvents(t, e, in("x"))
	var seen []events.Event
	for ev := range sub.Events() {
		seen = append(seen, ev)
		if ev.Type == "analyzer.started" && data(ev)["analyzer"].(job.AnalyzerRun).ID == "news_search" {
			break
		}
	}
	<-started
	late, err := e.Subscribe(j.ID)
	require.NoError(t, err)
	snap := data(<-late.Events())
	require.Equal(t, planOf(seen), snap["plan"])
	require.Equal(t, progressOf(seen[len(seen)-1]), snap["progress"])
	require.Equal(t, job.Progress{Done: 1, Running: []string{"news_search"}, Total: 3}, *snap["progress"].(*job.Progress))
	var tp any
	for _, ev := range seen {
		if ev.Type == "analyzer.succeeded" {
			tp = data(ev)["preview"]
		}
	}
	require.Equal(t, map[string]any{"translation": tp}, snap["previews"])
	require.Equal(t, map[string]any{"source_language": "de", "translated": true}, tp)
	acts := snap["activities"].(map[string]job.Activity)
	require.Equal(t, map[string]job.Activity{"news_search": data(seen[len(seen)-1])["activity"].(job.Activity)}, acts)
	require.Equal(t, "Searching news coverage", acts["news_search"].Text)
	_, hasJob := snap["job"].(job.Job)
	require.True(t, hasJob)
	late.Close()
	close(release)
	drain(t, sub)

	// the Job object has no event-stream members
	b, _ := json.Marshal(waitDone(t, e, j.ID))
	for _, k := range []string{"preview", "plan", "progress", "previews", "activities"} {
		require.NotContains(t, string(b), `"`+k+`"`)
	}
}

func TestSnapshotBeforePlan(t *testing.T) {
	inflight := make(chan struct{})
	sc := stubScraper(func(ctx context.Context, _ ss.Target, _ ss.Request) (*ss.Result, error) {
		close(inflight)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	e := newEngine(t, scrapeRegistry, Config{Scraper: sc}, map[string]adapter.Handler{"ok": ok, "final": ok})
	j, _ := createWithEvents(t, e, scrapeReq("https://mastodon.social/@alice"))
	<-inflight
	sub, err := e.Subscribe(j.ID)
	require.NoError(t, err)
	snap := data(<-sub.Events())
	b, err := json.Marshal(snap)
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	require.JSONEq(t, "null", string(m["plan"]))
	require.JSONEq(t, "null", string(m["progress"]))
	require.JSONEq(t, "{}", string(m["previews"]))
	require.JSONEq(t, "{}", string(m["activities"]))
	_, _ = e.Cancel(j.ID)
}

// Additive only: every pre-existing event member and the REST Job keep their shape and order.
func TestExistingMembersAndRESTUnchanged(t *testing.T) {
	e := newEngine(t, regYAML(an("a", "ok"), an("b", "ok", deps("a")), finDef), Config{Finalizer: &fakeFinalizer{}}, map[string]adapter.Handler{"ok": ok, "final": ok})
	jb, sub := createWithEvents(t, e, in("x"))
	evs := drain(t, sub)
	require.Equal(t, []string{"job.snapshot", "job.started", "analysis.planned", "analyzer.started:a", "analyzer.succeeded:a",
		"analyzer.started:b", "analyzer.succeeded:b", "analysis.ready_for_finalization", "analysis.finalizing", "analysis.finalized", "job.completed"}, typesFor(evs))
	has := func(i int, members ...string) {
		for _, m := range members {
			require.Contains(t, data(evs[i]), m, evs[i].Type)
		}
	}
	has(0, "job")
	has(1, "job_id")
	has(3, "analyzer", "activity", "progress")
	has(4, "analyzer", "progress", "preview")
	has(7, "coverage")
	has(8, "finalization", "activity", "progress")
	has(9, "finalization", "progress")
	has(10, "job")
	require.NotContains(t, data(evs[7]), "progress", "other events are untouched")
	require.NotContains(t, data(evs[10]), "progress")
	require.Equal(t, waitDone(t, e, jb.ID), data(evs[10])["job"])

	b, err := json.Marshal(waitDone(t, e, jb.ID))
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	var got []string
	for k := range m {
		got = append(got, k)
	}
	require.ElementsMatch(t, []string{"id", "status", "done", "created_at", "started_at", "finished_at", "input", "scrape", "options", "public",
		"analyzers", "coverage", "finalization"}, got)
	var run map[string]json.RawMessage
	var runs []json.RawMessage
	require.NoError(t, json.Unmarshal(m["analyzers"], &runs))
	require.NoError(t, json.Unmarshal(runs[0], &run))
	got = nil
	for k := range run {
		got = append(got, k)
	}
	require.ElementsMatch(t, []string{"id", "name", "category", "depends_on", "status", "reason", "error", "started_at", "finished_at",
		"duration_ms", "result", "availability", "note"}, got)
}
