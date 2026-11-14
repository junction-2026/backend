# Design

A queued job's subscriber sees everything from `job.started` on (engine_test.go
TestGlobalJobLimitQueues). With `MaxParallelJobs: 1` a gate job (all analyzers skipped,
finalization held in `gatedFinalizer.Run` until its channel is closed) keeps the test job
queued while `create()` subscribes. The wait is on a channel, not on time; the hold is not
bound to the finalizer timeout (TestTimeout uses 150ms). Works for the zero-analyzer case since
no handler is needed. The gate channel is released with `defer`, also when `create()` fails.

Rejected: wrapping analyzer handlers with a start gate (cannot cover `job.started`, which is
emitted before any handler, nor the zero-analyzer case); sleeps/retries.

TestNotConfigured has no finalizer to gate and asserts no events, so it uses `Create` + `waitDone`.

## Race evidence (50ms `time.Sleep` between Create and Subscribe, not committed)

- old helper: FAIL TestSuccessNormalizesAndKeepsUnknownMembers, TestFinalizeFalse,
  TestInsufficientEvidenceShortCircuit, TestSlowAnalyzerDelaysOnlyFinalization ("fast analyzer never settled")
- new helper, same delay: `go test -race ./internal/finalizer/` ok
