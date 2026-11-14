# Proposal: backend analysis finalizer

## Why

Parent feature junction-2026/project#2 needs a final synthesis after all analyzers
settled. The engine (`backend-analysis-engine`) already owns the settle barrier, the
`Finalizer` hook, timeout and cancellation; this change adds the runtime behind that
hook (child `backend-finalizer`, issue junction-2026/backend#3): the finalizer service
call, response normalization and validation, and the insufficient-evidence short-circuit.

Implements the Finalization, FinalAssessment and Insufficient evidence requirements of
`analysis-jobs-api`, the finalization events of `analysis-job-events` and the Finalizer
response requirements of `analyzer-service-protocol` from the approved shared change
`add-analysis-orchestrator-api` at shared-specs @ 42d691c711728b90ba4ee126ce99869e80292790.
It does not restate them. The scoring methodology is external (`FINALIZER_URL`).

## What Changes

- New backend-local capability `analysis-finalizer` (`internal/finalizer`).
- Engine-adjacent change: the registry default `execution.timeout` of an
  `after_all_analyzers_settled` analyzer is 60s (other analyzers keep 30s).
- Example registry entry `docs/examples/final_synthesis.yaml`.
- The single registration line in `cmd/orchestrator/main.go` is deferred until
  backend-api lands (`finalizer.New(reg, exec, log)` returns the value for
  `engine.Config.Finalizer`).

## Capabilities

### New Capabilities
- `analysis-finalizer`

### Modified Capabilities
- none

## Impact

- New package `internal/finalizer`; one-line default change in `internal/registry`.
- No new dependencies. Not breaking.
