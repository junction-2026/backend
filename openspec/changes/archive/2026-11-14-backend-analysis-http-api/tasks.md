# Tasks

## 1. HTTP API
- [x] 1.1 `internal/httpapi`: error envelope, route table with 404/405 envelopes, panic recovery, request logging
- [x] 1.2 Create/get/cancel job handlers, body limit and validation
- [x] 1.3 List jobs with cursor pagination and status filter, list analyzers, `/healthz`

## 2. WebSocket
- [x] 2.1 `internal/ws`: upgrade, snapshot and event forwarding, close codes 1000/1001/4001
- [x] 2.2 Ping, write timeout, client message discard, graceful close of all connections

## 3. Process and image
- [x] 3.1 `cmd/orchestrator`: config from environment, wiring, marked finalizer registration point, graceful shutdown
- [x] 3.2 `Dockerfile` and sample analyzers directory with `text_stats` only

## 4. Verification
- [x] 4.1 httpapi tests with a fake service for every endpoint and error case
- [x] 4.2 WebSocket tests with a real client (late subscriber, finished job, ordering, slow consumer, shutdown)
- [x] 4.3 REST/event consistency test, no-secret test for `GET /api/v1/analyzers`
- [x] 4.4 End-to-end test of the server with `text_stats`
- [x] 4.5 `go test ./... -race -count=1`, `scripts/validate-openspec.sh`, `docker build`
