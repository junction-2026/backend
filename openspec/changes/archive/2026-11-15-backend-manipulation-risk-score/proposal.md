# Proposal: Manipulation Risk Score backend (junction-2026/project#64)

## Why
Shared change `manipulation-risk-score` (shared-specs @ 3949bec3d32a399427d239e3147239fa1e716cfe) adds `Job.risk` and `data.risk` on
`analysis.ready_for_finalization`. This is the backend side: configuration, bot-detector mapping, deterministic computation,
engine hook and persistence.

## What Changes
- New package `internal/risk`: configuration (`RISK_SCORE_CONFIG` YAML, shared defaults, start-up validation), the bot_detection result
  mapping, the pure computation `Compute(job, settings)` and the fixed English texts (`texts.go`).
- `job.Job` gains `Risk` (raw JSON, null until the barrier).
- Engine computes the RiskScore once inside the settle barrier, before emitting `analysis.ready_for_finalization`.
- Both stores persist `Job.risk` verbatim through the existing Job JSON; no migration.
- `config/risk_score/risk_score.yaml` example and mapping comments in `docs/examples/bot_detection.yaml`.

## Contracts
Shared: `manipulation-risk-score`, `analysis-jobs-api` (Job risk member), `analysis-job-events` (Risk score with the settle barrier),
all at the pinned SHA. Nothing is duplicated here; the local specs cover configuration, mapping, structure and tests only.
