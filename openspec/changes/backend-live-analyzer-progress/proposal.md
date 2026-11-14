# Proposal: live analyzer progress events

## Why

Parent feature junction-2026/project#30, child issue junction-2026/backend#93. The event stream tells a client only
that an AnalyzerRun changed state. The backend now emits a run plan, user-facing activities, progress counters and
compact previews of each analyzer's result, and carries them in `job.snapshot` so a late subscriber can rebuild the
view.

## What Changes

- Engine: `analysis.planned`, `activity` and `progress` on `analyzer.started`, `progress` on every settle event after
  the plan, `availability` and `note` on `failed` and `skipped`, `preview` on `succeeded`, `activity` and `progress`
  on `analysis.finalizing`, `progress` on `analysis.finalized` and `analysis.finalization_failed`, `activity` on
  `scrape.started` (`internal/engine/plan.go`, `activity.go`, `preview.go`, `run.go`, `scrape.go`).
- Job record: plan, Progress, per-analyzer Activity and preview, guarded by the job mutex (`internal/job/progress.go`);
  `job.snapshot` gets `plan`, `progress`, `previews` and `activities` beside `data.job`.
- Registry: optional `ui` block (`hidden`, `activity_key`); `text_stats` is hidden by default.
- Adapters: the decisions an activity names (news_search languages, fact_check qualifying claims, techniques model
  selection, llm_techniques headline check) are exported from `internal/adapter/activity_facts.go` and used by the
  adapters themselves.
- README: event documentation.

## Shared specs

Pinned `junction-2026/shared-specs@052e3685233b676ef61999d78748c170e360b73a`, change `live-analyzer-progress`
(capability `analysis-job-events`). Event members, Activity, Progress, PlanEntry and the preview shapes are defined
there and implemented here without restating them. The requirements in this change are backend-local: registry
`ui` settings, the job record state, text and language tables, how activity facts are computed, preview builder
robustness and tests.

## Impact

`GET /api/v1/jobs/{id}`, the Job object and `GET /api/v1/analyzers` are unchanged. No LLM, translation or network
call is added.
