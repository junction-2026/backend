## Purpose

Backend-local requirements for the `internal/finalizer` test helpers: event subscriptions in those tests must be race-free so that event-based assertions are deterministic regardless of goroutine scheduling.

## ADDED Requirements

### Requirement: Subscription before first event
The finalizer test helper that creates a job and subscribes SHALL subscribe before the job can emit any event the calling test asserts on, so that `job.snapshot` then `job.started` and all later events are delivered regardless of goroutine scheduling. The guarantee holds while the subscription is made within the gate job's finalizer timeout (5s for event-asserting tests); tests with a shorter timeout SHALL NOT assert on events.

#### Scenario: Subscriber is delayed
- **WHEN** a delay shorter than the finalizer timeout is inserted between job creation and subscription
- **THEN** the subscription still receives `job.snapshot`, `job.started` and every later event

### Requirement: Synchronization-only ordering
The helper SHALL NOT rely on sleeps, retries or timing windows; ordering SHALL be enforced by synchronization (channels, gates or job queueing).

#### Scenario: Slot held by a gate job
- **WHEN** the helper creates a job
- **THEN** the job is queued behind a gate job until the subscription exists and the gate channel is closed

### Requirement: Event assertions use the helper
Tests in `internal/finalizer` that assert event presence, counts or order SHALL do so only on subscriptions created through the race-free helper.

#### Scenario: Call sites
- **WHEN** a test asserts on events
- **THEN** its subscription comes from `create()` on an engine built by `newEngine`

### Requirement: Slow analyzer delays only finalization
`TestSlowAnalyzerDelaysOnlyFinalization` SHALL assert: no finalizer call and no `analysis.ready_for_finalization` while the slow analyzer is held; the job status is `running` at that point; `analysis.ready_for_finalization` comes after the last `analyzer.succeeded`; exactly one finalizer call; exactly two `analyzer.succeeded` events.

#### Scenario: Slow analyzer held then released
- **WHEN** the fast analyzer succeeded and the slow one is held, then released
- **THEN** the stated assertions hold

### Requirement: Test-only fix
The fix SHALL be test-only: production code in `internal/engine`, `internal/events` and `internal/finalizer` is unchanged and the snapshot-plus-later-events subscription semantics are kept.

#### Scenario: Diff scope
- **WHEN** the change is merged
- **THEN** only `_test.go` files and this OpenSpec change differ
