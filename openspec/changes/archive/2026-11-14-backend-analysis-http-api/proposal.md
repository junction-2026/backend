# Proposal: backend analysis HTTP API

## Why

Parent feature junction-2026/project#2 needs the analysis engine reachable over the
network. This change (child `backend-api`) adds the HTTP server on top of the engine
from `backend-analysis-engine`: the REST jobs API, the WebSocket job event stream, the
process entry point and the container image.

Based on backend @ 4070afae5b8bbec7510fca2848a1f0c33e55d308 (engine) and the approved
shared change `add-analysis-orchestrator-api` at shared-specs @
42d691c711728b90ba4ee126ce99869e80292790. The endpoints, objects, status codes, error
codes and events are defined by the shared capabilities `api-conventions`,
`analysis-jobs-api` and `analysis-job-events`; this change implements them and does
not restate them.

## What Changes

- New backend-local capability **orchestrator-http-server**: configuration, health
  endpoint, request body and cursor handling, graceful shutdown, WebSocket delivery
  limits, logging rules, container image and the required tests (runtime behaviour that
  is not an interface between repositories, per common rule 13).
- New packages `internal/httpapi` (REST handlers, error envelope, pagination),
  `internal/ws` (event stream endpoint) and `cmd/orchestrator` (main, config, wiring),
  plus `Dockerfile` and a sample analyzers directory.
- No shared contract change is needed.

Out of scope: engine packages and `internal/finalizer` (backend-finalizer registers its
finalizer in the one marked place in `cmd/orchestrator/main.go`).

## Capabilities

### New Capabilities
- `orchestrator-http-server`

### Modified Capabilities
- none

## Impact

- New dependency `github.com/coder/websocket`.
- New `Dockerfile` and `config/analyzers/text_stats.yaml`.
- Compatibility: nothing existed before, so nothing is breaking.
