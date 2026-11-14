# analyzer-adapters Specification

## Purpose
Backend-local behaviour of the analyzer adapters that execute one analyzer: the http_sync and http_async adapters following the shared analyzer-service-protocol, the in-process custom adapter with its handler registry and built-in handler, and input binding resolution.

## Requirements

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

### Requirement: Propaganda detection request
The `propaganda_detection` adapter SHALL send one `POST` to the configured endpoint with the JSON body `{"text": <the job input's normalized_content>}` and the headers `Content-Type: application/json`, `Accept: application/json`, `X-Analysis-Job-Id`, `X-Analysis-Analyzer-Id` plus the configured headers. It SHALL NOT retry and SHALL use the no-redirect client.

#### Scenario: Request shape
- **WHEN** the adapter runs for job `j1` with normalized content `Fake news!` and an `Authorization` header configured
- **THEN** exactly one `POST` arrives with body `{"text": "Fake news!"}`, `X-Analysis-Job-Id: j1`, `X-Analysis-Analyzer-Id` and the `Authorization` header

#### Scenario: Service error is not retried
- **WHEN** the service answers 502
- **THEN** the adapter fails with `adapter_error` after exactly one request

### Requirement: Propaganda detection response
A 2xx response SHALL be accepted only when its body is a JSON object whose `result` is a non-empty array of objects, each with a `label` that is `PROPAGANDA` or `NO_PROPAGANDA` and a numeric `score` in [0, 1], and whose `latency_ms`, when present, is a non-negative number. The object SHALL then be returned verbatim, unknown members kept, as the analyzer result.

#### Scenario: Valid response
- **WHEN** the service answers 200 `{"result": [{"label": "PROPAGANDA", "score": 0.97}], "latency_ms": 12.3, "model": "m"}`
- **THEN** that object is the analyzer result, including `model`

#### Scenario: Invalid response
- **WHEN** the body is not a JSON object, `result` is missing, not an array or empty, an element is not an object, a `label` is another value, a `score` is not a number in [0, 1] or `latency_ms` is present but negative or not a number
- **THEN** the adapter fails with `adapter_error`

### Requirement: Propaganda detection failures
Any other body, a non-2xx status (including 3xx) and a connection error SHALL fail the `propaganda_detection` adapter with `adapter_error`. A configured `output.schema` SHALL still be applied to the stored result after the adapter's own validation.

#### Scenario: Redirect
- **WHEN** the endpoint answers 3xx
- **THEN** the redirect is not followed, the target receives no request and the adapter fails with `adapter_error`

#### Scenario: Output schema
- **WHEN** the adapter result violates the analyzer's `output.schema`
- **THEN** the analyzer fails with `invalid_output`

### Requirement: Propaganda detection abort and messages
When the context ends while the `propaganda_detection` adapter waits for the service, it SHALL fail with `timeout` or `cancelled` according to the context and return within 2 seconds of the context ending. Its error messages SHALL NOT contain the endpoint or configured header values.

#### Scenario: Timeout and cancel
- **WHEN** the service does not answer until the timeout elapses or the job is cancelled
- **THEN** the adapter fails with `timeout` or `cancelled` respectively within 2 seconds

#### Scenario: No endpoint or credentials in messages
- **WHEN** the adapter fails because the service is unreachable or answers 500
- **THEN** the error message contains neither the endpoint nor a configured header value

### Requirement: LLM request
The `llm` adapter SHALL send `POST {base_url with trailing slashes trimmed}/chat/completions` with the configured headers, then `Content-Type: application/json` and `Authorization: Bearer <api_key>`, so configured headers cannot override those two. The body SHALL have `model`, `temperature`, `max_tokens`, `messages` (`system`: prompt file content, `user`: the assembled message) and `response_format` by mode, with the `extra_body` members merged at top level last.

#### Scenario: Configured request
- **WHEN** the finalizer has `temperature: 0.2`, `max_tokens: 123`, `headers: {X-Title: Junction, Authorization: evil}` and `extra_body: {top_p: 0.5}`
- **THEN** the request has those values, `X-Title: Junction`, `top_p: 0.5` and `Authorization: Bearer <api_key>`

### Requirement: LLM finalization schema
The finalization schema S SHALL be an object schema with properties `verdict` (enum `detected`, `not_detected`, `uncertain`), `confidence` (type `["number","null"]`, 0 to 1) and `user_text` (string, minLength 1), `required` all three and `additionalProperties: false`.

#### Scenario: Schema sent
- **WHEN** the finalizer runs in `json_schema` mode
- **THEN** `response_format.json_schema.schema` equals S

### Requirement: LLM response formats
In `json_schema` mode `response_format` SHALL be `{"type":"json_schema","json_schema":{"name":"finalization","strict":true,"schema":S}}`. In `json_object` mode it SHALL be `{"type":"json_object"}` and the user message SHALL end with `\n<schema>\nS\n</schema>`. In `none` mode the body SHALL have no `response_format` member and the user message ends with the schema the same way.

#### Scenario: Each mode
- **WHEN** the finalizer runs once in each of the three modes against a stub
- **THEN** each request body matches its mode as described

### Requirement: LLM reply decoding
The adapter SHALL parse `choices[0].message.content` as a JSON object, tolerating one surrounding code fence (three backticks, optionally followed by `json`). Missing or empty `choices`, a `finish_reason` other than `stop`, a non-empty `message.refusal`, missing or blank content, or content that is not a JSON object SHALL fail with `invalid_output`. The provider's `usage` object SHALL be returned verbatim, or null when absent.

#### Scenario: Fenced reply
- **WHEN** the content is a JSON object inside a code fence labelled `json`
- **THEN** finalization completes with that object

#### Scenario: Truncated reply
- **WHEN** `finish_reason` is `length`
- **THEN** finalization is `failed` with `invalid_output`

#### Scenario: Refusal
- **WHEN** the message has `refusal: "I can't help"` and null content
- **THEN** finalization is `failed` with `invalid_output`

### Requirement: LLM retries
HTTP 429, 5xx and connection errors before a response SHALL get exactly one retry after a 1s backoff (for 429, `Retry-After` seconds up to 5s), within the finalizer context. Other non-2xx statuses SHALL fail at once with `adapter_error`. The default client SHALL NOT follow redirects.

#### Scenario: 429 then success
- **WHEN** the provider answers 429 then 200 with a valid reply
- **THEN** finalization completes after two requests

#### Scenario: 500 twice
- **WHEN** the provider answers 500 twice
- **THEN** finalization is `failed` with `adapter_error` after two requests

### Requirement: LLM provider errors
A non-2xx answer SHALL fail with `adapter_error` and the message `llm provider HTTP <status>`, followed by `: <provider error.message>` when present, with every occurrence of the API key and of each configured header value of 4 or more bytes replaced by `[REDACTED]` before truncation to 300 characters. Messages SHALL NOT contain `base_url`.

#### Scenario: 400
- **WHEN** the provider answers 400 with `{"error":{"message":"bad model"}}`
- **THEN** finalization is `failed` with `adapter_error`, message `llm provider HTTP 400: bad model`, after one request

#### Scenario: Key echoed
- **WHEN** the provider answers 401 with a message that repeats the API key
- **THEN** `error.message` has `[REDACTED]` in place of each occurrence

#### Scenario: Header value echoed
- **WHEN** the finalizer has `headers: {X-Proxy-Token: proxy-SECRET-42}` and the provider's error message repeats that value
- **THEN** `error.message`, logs and Job and event JSON do not contain it

### Requirement: LLM timeout and cancellation
The adapter SHALL run under the finalizer context, including the retry backoff. Expiry SHALL give `timeout` and job cancellation `cancelled`.

#### Scenario: Reply after the timeout
- **WHEN** the provider does not answer within the finalizer timeout
- **THEN** finalization is `failed` with `timeout` and the job ends `completed`
