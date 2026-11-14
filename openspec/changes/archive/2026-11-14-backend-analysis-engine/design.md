# Design: backend analysis engine

## Package layout and API for the sibling children

- `internal/job`: `Job`, `Input`, `AnalyzerRun`, `Coverage`, `Finalization`,
  `FinalAssessment` with JSON tags of the shared objects; `Record` = a stored Job plus
  its mutex; `RunError{Code,Message}` is also the error type adapters return.
- `internal/registry`: `Load(path, Options)` and the immutable `Registry`.
- `internal/adapter`: `Executor.Execute(ctx, def, Request)` (all three adapters),
  `Inputs(def, job)` (binding resolution, reused by the finalizer for its request),
  `Handlers` (in-process handler registry).
- `internal/events`: `Bus` (`Publish`, `Subscribe`, `Close`, `Remove`).
- `internal/store`: `Store` interface and the in-memory implementation.
- `internal/engine`: `Engine` with `Create`, `Get`, `List`, `Cancel`, `Subscribe`,
  `Sweep`, `Close`; `Finalizer` and `Executor` interfaces; typed errors
  `UnknownAnalyzerError`, `NotSelectableError`, `ErrNotFound`, `ErrJobFinished`.

## Decisions

### D1. One mutex per job serializes transitions and event emission
Every state change and the event describing it happen inside the job's lock, and the
bus is only called from inside it. Lock order is always job lock then bus lock; the
bus never calls back. `Subscribe` takes the job lock, so the snapshot and the
subscription registration are atomic with respect to every event, and a REST read
(`Get`, same lock) never sees a state ahead of or behind the emitted events.

### D2. Barrier by evaluation under the lock
After every settle the scheduler, still inside the lock, resolves skip propagation to a
fixpoint and then checks `settled == total`. A `barrier` flag under the same lock makes
`analysis.ready_for_finalization` fire exactly once, after the last settle event, and
never after cancellation. If no finalization will run, `job.completed` follows in the
same critical section.

### D3. Cancellation settles immediately
`Cancel` settles running analyzers as `failed/cancelled` itself and cancels the job
context; adapter goroutines that return later find their run settled and drop the
result. So the cancel response and the event stream are deterministic and do not wait
for slow adapters. Adapters still honour the context (HTTP abort, `DELETE` for
`http_async`, 2s bound) so work stops promptly.

### D4. Finalizer hook scope
The engine owns the barrier, the `finalizing` transition, the timeout (from the
finalizer registry definition) and event emission. The `Finalizer` implementation
(binding the request, calling the adapter, validating the FinalAssessment, the
insufficient-evidence short-circuit) belongs to `internal/finalizer` and receives a
consistent clone of the job plus the finalizer definition. Finalization is configured
only when a `Finalizer` is registered and the registry has an enabled
`after_all_analyzers_settled` analyzer; otherwise `skipped/not_configured`.
`Finalization.result` stores the returned JSON verbatim (it only has to be an object).

### D5. Credentials are not forwarded to status URLs on other hosts
For `http_async`, configured headers (credentials) are sent to `status_url` only when
it has the same scheme and host as the configured endpoint, because the URL comes from
the service's response. Correlation headers are always sent.

### D6. Messages never contain endpoint URLs
Adapter error messages are fixed phrases ("analyzer service returned HTTP 503") so
`error.message` cannot leak configured endpoints or credentials (api-conventions).

### D7. Skipped-at-creation analyzers
They are `skipped` already in the created Job and get their `analyzer.skipped` event
right after `job.started`, which keeps "one settle event per analyzer" for jobs that
are cancelled while queued (their stream is `job.started`, the skips, the
cancellation settles, `job.cancelled`).

## Risks / Trade-offs

- A custom handler that ignores its context keeps its goroutine until it returns; the
  adapter returns on time anyway.
- Jobs are lost on restart (in-memory store, as the shared design states).
- A finalizer that ignores its context keeps its goroutine until it returns; the engine
  stops waiting at the timeout and drops the late result.
