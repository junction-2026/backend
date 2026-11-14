# Proposal: persistent job history with a SQLite store (backend)

## Why

Parent feature junction-2026/project#43, child issue junction-2026/backend#111. Implements the shared change
`persistent-job-history` (shared-specs @ b4a04a191450549e9d263166bd8cc2904044cff6, capabilities `analysis-jobs-api`
and `analysis-job-events`): a deployment can keep job history across restarts, jobs that were not done at the stop
end `failed` with `interrupted` parts, and the event stream of a persisted job is one `job.snapshot`. The observable
behaviour is the shared contract; store selection, schema, serialization, write points and recovery are
backend-local and specified in `specs/job-store`.

## What Changes

- `internal/store`: the `Store` interface gets a history part (`Save`, `Load`, `Page`, `Unfinished`, `Prune`,
  `Close`) next to the working set (`Put`, `Get`, `EvictFinished`); `Memory` stays the default and has no history;
  new `SQLite` store (modernc.org/sqlite, no CGO).
- `internal/engine`: writes the job at every event, `Recover` at startup, `Get`/`Page`/`Subscribe`/`Cancel` resolve
  persisted jobs, `Sweep` prunes history only with `JOB_HISTORY_RETENTION`.
- `internal/job`: `Record.Interrupt`, `interrupted` reason/code, `Record.LastSeq`.
- `internal/events`: `Bus.Closed` (snapshot-only stream at a given seq).
- `internal/httpapi`: `GET /jobs` pages through the store (same ordering, filter, cursor).
- `cmd/orchestrator`: `JOB_STORE`, `JOB_STORE_PATH`, `JOB_HISTORY_RETENTION`; README, Dockerfile note.

Not changed: shared specs, frontend, other analyzers; no Postgres, no sharing of a database between instances,
no import of old in-memory data.

## Impact

backend only. Default behaviour (memory) is unchanged. One new dependency: `modernc.org/sqlite` (pure Go; the Docker
build keeps `CGO_ENABLED=0`).
