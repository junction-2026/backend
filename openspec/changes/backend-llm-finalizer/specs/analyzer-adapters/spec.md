## ADDED Requirements

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
