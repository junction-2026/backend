# Proposal: fix finalizer test subscribe-after-create race

## Why

Parent feature junction-2026/project#3. `create()` in `internal/finalizer/finalizer_test.go`
called `Engine.Create` (which starts the job) before `Engine.Subscribe`. A subscription gets
`job.snapshot` plus only later events (intended `events.Bus` behaviour), so events emitted in
between were missing and `TestSlowAnalyzerDelaysOnlyFinalization` (and others) were flaky.

## What Changes

- Test-only. `create()` subscribes before the job can start: the engine in tests runs one job
  at a time and `create()` first queues a gate job that holds the slot inside the finalizer
  hook (`gatedFinalizer`, `gate_test.go`); the test job stays queued, `create()` subscribes,
  then releases the gate.
- `TestSlowAnalyzerDelaysOnlyFinalization` asserts on the last `analyzer.succeeded`.
- `run()` takes the job id from `create()` instead of the last event.
- No shared-specs change; no production code change.

## Capabilities

### New Capabilities
- `finalizer-test-harness`

### Modified Capabilities
- none

## Impact

`internal/finalizer/finalizer_test.go`, new `internal/finalizer/gate_test.go`.
