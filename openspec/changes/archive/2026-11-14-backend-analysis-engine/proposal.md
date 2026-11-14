# Proposal: backend analysis engine

## Why

Parent feature junction-2026/project#2 needs a backend orchestrator that runs
declaratively configured analyzers as a dependency DAG and streams results as they
settle. This change is the runtime foundation (child `backend-engine`): job model,
registry, adapters, scheduler, event bus and store. It has no HTTP handlers and no
finalizer runtime; the API and finalizer children build on the engine's Go API.

Based on backend @ f7d36425060767d1069efd3e086ac60d388c159f (empty Go-service
scaffold) and the approved shared change `add-analysis-orchestrator-api` at
shared-specs @ 42d691c711728b90ba4ee126ce99869e80292790 (capabilities
`analysis-jobs-api`, `analysis-job-events`, `analyzer-service-protocol`,
`api-conventions`). The engine implements those contracts; it does not restate them.

## What Changes

New backend-local capabilities (runtime, storage and file formats are not shared
interfaces, per common rule 13):

- **analyzer-registry**: YAML registry loaded from `ANALYZERS_CONFIG`, definition
  fields, `${VAR}` expansion, validation rules.
- **analyzer-adapters**: the `http_sync`, `http_async` and `custom` adapters (the
  shared `analyzer-service-protocol` is the wire contract), the in-process handler
  registry and the built-in `text_stats` handler, output-schema validation.
- **analysis-engine**: job creation (selection, applicability, normalized content),
  concurrency limits, scheduling, timeouts, cancellation, the settle barrier and
  `Finalizer` hook, the per-job event bus, the in-memory store and eviction.

Out of scope: `internal/httpapi`, `internal/ws`, `cmd/` (backend-api child) and
`internal/finalizer` (backend-finalizer child). With no `Finalizer` registered, a job
completes with finalization `skipped` / `not_configured`.

## Capabilities

### New Capabilities
- `analyzer-registry`
- `analyzer-adapters`
- `analysis-engine`

### Modified Capabilities
- none

## Impact

- New Go module `github.com/junction-2026/backend` with `internal/{job,registry,adapter,engine,store,events}`.
- Dependencies: `gopkg.in/yaml.v3`, `github.com/santhosh-tekuri/jsonschema/v6`,
  `golang.org/x/net/html`, `golang.org/x/text/unicode/norm`, `github.com/google/uuid`;
  `github.com/stretchr/testify` for tests.
- Compatibility: nothing exists yet, so nothing is breaking.
