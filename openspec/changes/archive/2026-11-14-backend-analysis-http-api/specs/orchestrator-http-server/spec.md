## Purpose

Backend-local runtime behaviour of the orchestrator HTTP server: configuration and startup, health endpoint, request body and cursor handling, graceful shutdown, WebSocket delivery limits, logging rules, the container image and the test obligations. The endpoints themselves are defined by the shared capabilities.

## ADDED Requirements

### Requirement: Server configuration and startup
The server SHALL listen on the address in `LISTEN_ADDR` (default `:8080`) and SHALL load the analyzer registry from `ANALYZERS_CONFIG` (default `/etc/orchestrator/analyzers`). A configuration error (unreadable or invalid registry, invalid engine setting, unusable listen address) SHALL make the process print a clear message naming the problem to standard error and exit with a non-zero status without serving requests.

#### Scenario: Defaults
- **WHEN** neither variable is set
- **THEN** the server listens on `:8080` and loads `/etc/orchestrator/analyzers`

#### Scenario: Invalid registry
- **WHEN** `ANALYZERS_CONFIG` names a directory without analyzer files
- **THEN** the process exits non-zero and the message names the path

#### Scenario: Job completes without finalizer
- **WHEN** the server runs with the built-in `text_stats` analyzer and no finalizer registered
- **THEN** a created job ends `completed` with `text_stats` `succeeded` and `finalization.status` `skipped`

### Requirement: Health endpoint
`GET /healthz` SHALL respond 200 with `{"status":"ok"}` once the registry is loaded. It is an operational endpoint outside `/api/v1` and not part of the shared contract.

#### Scenario: Healthy
- **WHEN** a client calls `GET /healthz` on a running server
- **THEN** the status is 200 and the body is `{"status":"ok"}`

### Requirement: Request body handling
Request bodies SHALL be limited to 1 MiB with `http.MaxBytesReader`. JSON decoding SHALL ignore unknown members and SHALL distinguish syntax errors (400 `invalid_request`) from type and validation errors (422 `validation_failed` with `details.fields`).

#### Scenario: Oversized body
- **WHEN** a client posts a body larger than 1 048 576 bytes to `/api/v1/jobs`
- **THEN** the status is 413 with `error.code` `payload_too_large`

#### Scenario: Syntax versus type error
- **WHEN** a client posts `{"input":` and then `{"input": {"content": 5}}`
- **THEN** the first yields 400 `invalid_request` and the second 422 `validation_failed`

### Requirement: Pagination cursors
Pagination cursors SHALL be opaque base64url-encoded `(created_at, id)` pairs naming the last item of the previous page. A cursor that is not valid base64url or does not contain such a pair SHALL yield 400 `invalid_request`.

#### Scenario: Pages do not overlap
- **WHEN** a client lists 5 jobs with `limit=2` and follows `next_cursor` until it is `null`
- **THEN** it receives every job exactly once, newest first

#### Scenario: Malformed cursor
- **WHEN** a client calls `GET /api/v1/jobs?cursor=%21%21`
- **THEN** the status is 400 with `error.code` `invalid_request`

### Requirement: Graceful shutdown
On SIGTERM (or SIGINT) the server SHALL stop accepting requests, close every open WebSocket connection with code 1001, cancel running jobs and exit within 10 seconds.

#### Scenario: Shutdown with a subscriber
- **WHEN** the process receives SIGTERM while a client is subscribed to a running job
- **THEN** the client's connection closes with code 1001 and the process exits within 10 seconds

### Requirement: WebSocket delivery limits
Each WebSocket connection SHALL buffer at most 256 undelivered events. When the buffer overflows the server SHALL close the connection with code 4001 and reason `slow_consumer`. The server SHALL send a ping every 25 seconds and SHALL abandon a frame write that does not complete within 10 seconds.

#### Scenario: Client does not read
- **WHEN** a client subscribes and stops reading while more than 256 events are emitted
- **THEN** the connection ends with close code 4001 and reason `slow_consumer`

### Requirement: Logging
The server SHALL log with `log/slog` as JSON lines that carry `job_id` and `analyzer_id` where applicable. Logs MUST NOT contain request bodies, analyzer results, headers or analyzer endpoint URLs.

#### Scenario: Analyzer settled
- **WHEN** an analyzer fails with an error whose message contains its endpoint URL
- **THEN** the log line carries `job_id`, `analyzer_id` and the error code but not the message

### Requirement: Container image
The repository SHALL contain a multi-stage `Dockerfile` that builds a static binary into a non-root image exposing port 8080, with a sample analyzers directory at `/etc/orchestrator/analyzers` containing only the `text_stats` analyzer.

#### Scenario: Image runs
- **WHEN** the image is started without configuration
- **THEN** the server listens on 8080 and `GET /api/v1/analyzers` lists only `text_stats`

### Requirement: Test obligations
Handler tests SHALL use `httptest` and a fake analyzer service and cover every endpoint and error case. WebSocket tests (late subscriber, finished job, ordering, slow consumer) SHALL use a real WebSocket client against `httptest.Server`. A test SHALL read the REST state after each event and compare it with the event payload for the same entity, and a test SHALL configure a secret-looking `FINALIZER_URL` and assert that `GET /api/v1/analyzers` does not contain it.

#### Scenario: Suite passes
- **WHEN** `go test ./... -race -count=1` runs
- **THEN** all tests pass
