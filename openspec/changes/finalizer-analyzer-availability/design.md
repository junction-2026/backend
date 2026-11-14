## Context

Shared contract: shared-specs@c2c3466b90ac2115b0c17ba4162781d131fea85f change `analyzer-run-availability` (mapping table is normative there).

## Decisions

- `job.Availability(run, runs)` is the single mapping function. The engine calls it in `settleLocked` (every runtime settle: success, failure, adapter skip, dependency skip, cancellation, input_unavailable) and for creation-time skips; it stores the result on the run so REST, `analyzer.*` events and `$job.analyzers` share one value that never changes.
- Dependencies settle before dependents, so the dependency_skipped root is final when a run settles.
- The finalizer calls the same function on the runs it receives instead of reading the stored fields; both yield identical values for settled runs.
- Notes are fixed English constants in `internal/job`.
