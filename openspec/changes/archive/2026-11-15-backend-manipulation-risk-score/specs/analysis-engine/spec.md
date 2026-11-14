## ADDED Requirements

### Requirement: Risk score at the settle barrier
The engine SHALL compute the RiskScore exactly once, inside the settle barrier, set `Job.risk` and emit `analysis.ready_for_finalization` with `data.risk` equal to it, before finalization starts, and SHALL never recompute it. A job that never reaches the barrier (cancelled before it, failed scrape, interrupted by a restart) SHALL keep `risk` null. Event order, types and counts SHALL be unchanged.

#### Scenario: With a finalizer
- **WHEN** a job settles all analyzers and a finalizer is configured
- **THEN** the events are `analysis.ready_for_finalization` (with `data.risk` equal to `Job.risk`), `analysis.finalizing`, `analysis.finalized`, `job.completed`

#### Scenario: Without a finalizer
- **WHEN** no finalizer is configured
- **THEN** `analysis.ready_for_finalization` (with `data.risk`) is directly followed by `job.completed`, whose `data.job.risk` equals it

#### Scenario: Cancelled before the barrier
- **WHEN** a job is cancelled while an analyzer is running
- **THEN** its `risk` is null

#### Scenario: Interrupted by a restart
- **WHEN** a persisted job that had not reached the barrier is recovered
- **THEN** it is failed with `risk` null
