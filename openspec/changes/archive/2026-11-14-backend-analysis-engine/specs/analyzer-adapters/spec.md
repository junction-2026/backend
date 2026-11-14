## Purpose

Backend-local behaviour of the analyzer adapters that execute one analyzer: the http_sync and http_async adapters following the shared analyzer-service-protocol, the in-process custom adapter with its handler registry and built-in handler, and input binding resolution.

## ADDED Requirements

### Requirement: HTTP request shape
The `http_sync` and `http_async` adapters SHALL send the configured method to the endpoint with the body `{"job_id", "analyzer_id", "inputs"}` and the headers `Content-Type`, `Accept`, `X-Analysis-Job-Id`, `X-Analysis-Analyzer-Id` plus the configured headers, as the shared `analyzer-service-protocol` requires. Adapters SHALL NOT retry.

#### Scenario: Correlation headers
- **WHEN** the adapter calls analyzer `sentiment` for job `j1`
- **THEN** the request has `X-Analysis-Job-Id: j1`, `X-Analysis-Analyzer-Id: sentiment`, the JSON body and the configured `Authorization` header

### Requirement: Synchronous adapter
`http_sync` SHALL return a 2xx JSON object body as the result and fail with `adapter_error` on a non-2xx status, a connection error or a body that is not a JSON object.

#### Scenario: Sync success
- **WHEN** a service answers 200 `{"score": 12}`
- **THEN** the result is `{"score": 12}`

#### Scenario: Sync service error
- **WHEN** a service answers 503, a connection fails, or the body is an array
- **THEN** the adapter fails with `adapter_error`

### Requirement: Asynchronous adapter
`http_async` SHALL accept a 200 JSON object as an immediate result, or a 202 with an absolute http(s) `status_url` and then `GET` it every `poll_interval`: `succeeded` with a `result` object yields the result, `failed` and non-200 answers fail with `adapter_error`, other statuses count as `running`. Any other submission answer fails with `adapter_error`.

#### Scenario: Async success
- **WHEN** the service answers 202 with a `status_url` and polls return `pending`, an unknown status and then `succeeded` with a result
- **THEN** the result is the polled `result`

#### Scenario: Async submission and poll failures
- **WHEN** the submission answers 202 without a valid `status_url`, or a poll answers 500, or `failed`, or `succeeded` without a result object
- **THEN** the adapter fails with `adapter_error` and sends no `DELETE`

### Requirement: Asynchronous abort
When the context ends while `http_async` waits for the submission or polls, the adapter SHALL send one best-effort `DELETE` to `status_url` (once it is known), fail with `timeout` or `cancelled` according to the context, and return within 2 seconds of the context ending.

#### Scenario: Async abort
- **WHEN** polls keep returning `running` until the timeout or until the job is cancelled
- **THEN** the adapter sends exactly one `DELETE` to `status_url` and fails with `timeout` or `cancelled` respectively

### Requirement: Adapter messages and credentials
Adapter error messages SHALL NOT contain endpoint or status URLs, and the default HTTP client SHALL NOT follow redirects. Configured headers SHALL be sent to a `status_url` only when it has the same scheme and host as the configured endpoint.

#### Scenario: Redirect to another host
- **WHEN** a configured endpoint answers 307 to another host
- **THEN** the redirect is not followed, the other host receives no request and the adapter fails with `adapter_error`

#### Scenario: Status URL on another host
- **WHEN** an async service answers with a `status_url` on a different host and the analyzer has an `Authorization` header configured
- **THEN** the poll request to that host carries no `Authorization` header

#### Scenario: No endpoint in messages
- **WHEN** an analyzer fails because its service is unreachable
- **THEN** the error message does not contain the endpoint

### Requirement: Custom adapter
The `custom` adapter SHALL resolve its handler by name from an in-process registry, run it with the analyzer context and require its result to marshal to a JSON object. A handler error SHALL fail the analyzer with `adapter_error` (a returned `*RunError` keeps its code), a panic with `adapter_error`, and the adapter SHALL return within 2 seconds of context cancellation even if the handler ignores the context.

#### Scenario: Handler ignores context
- **WHEN** a handler blocks forever and the analyzer timeout elapses
- **THEN** the adapter fails with `timeout` within one second

#### Scenario: Non-object result
- **WHEN** a handler returns a list
- **THEN** the analyzer fails with `adapter_error`

### Requirement: Built-in text_stats handler
The engine SHALL ship the custom handler `text_stats` returning `{"characters", "words", "lines"}` of `normalized_content`: characters are Unicode code points, words are whitespace separated and `lines` is 0 for empty content.

#### Scenario: Built-in handler
- **WHEN** `text_stats` runs on the normalized content `Hello   world` and `second line` separated by a line feed
- **THEN** the result is `{"characters": 25, "words": 4, "lines": 2}`

### Requirement: Input bindings resolution
The inputs of an analyzer request SHALL contain one member per configured binding resolved from the sources `$input` (members `content`, `normalized_content`, `content_type`, `source`), `$input.source`, `$job.id`, `$analyzers.<id>` (the dependency's stored result), `$job.analyzers` and `$job.coverage`; with no bindings configured the inputs SHALL be `{"content": <$input>}`.

#### Scenario: Default inputs
- **WHEN** an analyzer has no bindings
- **THEN** its inputs are `{"content": {"content", "normalized_content", "content_type", "source"}}`

#### Scenario: Dependency result
- **WHEN** an analyzer binds `entities: "$analyzers.entities"` and `entities` returned `{"items": []}`
- **THEN** its inputs contain `entities` equal to `{"items": []}`
