## MODIFIED Requirements

### Requirement: LLM reply decoding
The adapter SHALL parse `choices[0].message.content` as a JSON object, tolerating one surrounding code fence (three backticks, optionally followed by `json`). Missing or empty `choices`, a `finish_reason` other than `stop`, a non-empty `message.refusal`, missing or blank content, or non-object content SHALL make the reply invalid, failing with `invalid_output` unless a reply retry ("LLM reply retry") succeeds. The provider's `usage` SHALL be returned verbatim, or null when absent.

#### Scenario: Fenced reply
- **WHEN** the content is a JSON object inside a code fence labelled `json`
- **THEN** finalization completes with that object

#### Scenario: Truncated reply
- **WHEN** `finish_reason` is `length` on both the first reply and the retry
- **THEN** finalization is `failed` with `invalid_output`

#### Scenario: Refusal
- **WHEN** the message has `refusal: "I can't help"` and null content
- **THEN** finalization is `failed` with `invalid_output` after one request

### Requirement: Topic extraction validation and normalization
The reply SHALL be decoded as for the finalizer ("LLM reply decoding") and validated per the shared "Topic extraction output validation"; a violation is a `schema` retry reason and, without a valid retry, fails the analyzer with `invalid_output` and `result: null`. The result SHALL be normalized per the shared "Topic extraction output normalization", with `usage` only from the provider's reports and `attempts` and `retry_reason` set by the adapter. Empty arrays SHALL be a success.

#### Scenario: Valid reply
- **WHEN** the reply has a topic with an extra member, a news item without `summary` and the provider reports usage
- **THEN** the result is `{topics, news, claims, input_truncated, usage, attempts, retry_reason}` in the reply's item order, without the extra member, with `summary: null`, `input_truncated: false`, that usage, `attempts: 1` and `retry_reason: null`

#### Scenario: Invalid replies
- **WHEN** the first reply and the retry are prose, lack `news`, have a non-object item, a blank `query` or a numeric `description`
- **THEN** the analyzer fails with `invalid_output`, the error message contains no credential, and other analyzers and finalization proceed as usual

#### Scenario: Empty lists
- **WHEN** the reply is `{"topics":[],"news":[],"claims":[]}`
- **THEN** the analyzer succeeds with empty `topics`, `news` and `claims`

#### Scenario: Reply members named attempts and retry_reason
- **WHEN** the first reply is `{"topics":[],"news":[],"claims":[],"reasoning":"...","attempts":7,"retry_reason":"x"}`
- **THEN** the result has `attempts: 1`, `retry_reason: null` and no `reasoning`

## ADDED Requirements

### Requirement: LLM reply retry
With `retry_invalid_output` enabled, every `llm` call (finalizer, `topic_extraction`, technique spans) SHALL send at most one reply retry when the first 2xx reply is invalid for a reason in "LLM reply retry reasons", independent of the 429/5xx/connection retry ("LLM retries"). The finalizer and `topic_extraction` SHALL report `attempts` 2 and the reason after a retry; technique-span results expose no new members.

#### Scenario: Cut-off reply retried
- **WHEN** the finalizer's first reply has `finish_reason: "length"` and the second is valid
- **THEN** finalization is `completed` with `attempts: 2` and `retry_reason: "length"`

#### Scenario: Prose reply retried
- **WHEN** the first reply's content is prose and the second is valid
- **THEN** the result has `attempts: 2` and `retry_reason: "invalid_json"`

#### Scenario: Missing user_text retried
- **WHEN** the first reply is `{"verdict":"detected","confidence":0.9}` and the second is valid
- **THEN** the result has `attempts: 2` and `retry_reason: "schema"`

#### Scenario: Configured schema failure retried
- **WHEN** the finalizer has an `output.schema` the first reply fails and the second reply satisfies it
- **THEN** the result has `retry_reason: "schema"`

#### Scenario: Topic without query retried
- **WHEN** the first `topic_extraction` reply has a topic without `query` and the second is valid
- **THEN** the analyzer succeeds with `attempts: 2` and `retry_reason: "schema"`

### Requirement: LLM reply retry reasons
The retry reasons SHALL be `length` (`finish_reason` `length`), `invalid_json` (empty content, or content that is not a JSON object after removing a code fence) and `schema` (a JSON object failing the caller's checks: the configured output schema or built-in checks such as empty `user_text` or a topic without `query`).

#### Scenario: Reason values
- **WHEN** the first reply is cut off, prose, or JSON without `user_text`
- **THEN** `retry_reason` is `length`, `invalid_json` or `schema` respectively

### Requirement: LLM reply retry exclusions
The adapter SHALL NOT send a reply retry for missing `choices`, a refusal (non-empty `message.refusal`), a `finish_reason` other than `stop` or `length` (e.g. `content_filter`), a non-2xx answer (HTTP 4xx other than 429 fails at once; 429/5xx keep their own retry), a timeout or a cancellation, when `retry_invalid_output` is false, or when less than `retry_min_remaining` remains before the call's deadline.

#### Scenario: Refusal, content_filter and HTTP 400
- **WHEN** the first answer is a refusal, has `finish_reason: content_filter` or is HTTP 400
- **THEN** the provider receives exactly one request

#### Scenario: Retry disabled
- **WHEN** `retry_invalid_output` is false and the first reply is prose
- **THEN** the provider receives one request and the call fails with `invalid_output`

#### Scenario: Too little time left
- **WHEN** `timeout` is 2s, `retry_min_remaining` is 5s and the first reply is prose
- **THEN** the provider receives one request and the call fails with `invalid_output`

### Requirement: LLM reply retry request
The retry request SHALL repeat the original messages and append one user message, and SHALL NOT contain the failed reply's content. For `length` it SHALL set `max_tokens` to `retry_max_tokens` and use the text "Your previous reply was cut off. Reply again with only the JSON object, and keep user_text within the word limit."; otherwise it SHALL keep `max_tokens` and use the text in "LLM reply retry reason text".

#### Scenario: Cut-off retry request
- **WHEN** `max_tokens` is 700, `retry_max_tokens` is 1500 and the first reply is cut off
- **THEN** the second request has `max_tokens: 1500`, the first request's messages and a final user message with the cut-off text

#### Scenario: Invalid-output retry request
- **WHEN** the first reply is prose containing a marker string
- **THEN** the second request ends with the invalid-output message whose reason is `llm reply content is not a JSON object` and contains no marker string

### Requirement: LLM reply retry reason text
For reasons other than `length` the message SHALL be "Your previous reply was not a valid JSON object matching the required schema (<reason>). Reply again with only that JSON object.", where `<reason>` is the first failure's message cut before its first `": "` (after which a schema error may quote reply values) and to at most 120 characters.

#### Scenario: Schema error with a quoted value
- **WHEN** the reason is `output violates schema at /user_text: 'abc' does not match pattern`
- **THEN** `<reason>` is `output violates schema at /user_text`

### Requirement: LLM reply retry model and time
When `retry_model` is non-empty the retry SHALL use that model with the same `base_url`, `api_key` and headers. The retry SHALL run under the same context and timeout as the first request.

#### Scenario: Retry model
- **WHEN** `retry_model` is `gpt-big` and the first reply is prose
- **THEN** the second request uses model `gpt-big` with the same `base_url` and `Authorization` header

### Requirement: LLM reply retry failure
When the retry's reply is invalid too, the call SHALL fail with `invalid_output`, the message `first: <first reason>; retry: <retry reason>` (prefixed with `invalid topic extraction reply: ` for `topic_extraction`) and no reply content. A transport failure of the retry SHALL fail the call with its own error.

#### Scenario: Both replies invalid
- **WHEN** the first reply is cut off and the second is prose
- **THEN** finalization is `failed` with `invalid_output` and message `first: llm reply finish_reason is "length", not "stop"; retry: llm reply content is not a JSON object`, and every AnalyzerRun keeps its result

### Requirement: LLM reply retry usage
After a successful retry, `usage` SHALL combine both reports: numeric members summed exactly, nested objects combined recursively, a member present in only one report kept, a non-numeric member taking the retry's value (a null in the retry keeps the first value), and null only when neither attempt reported usage.

#### Scenario: Summed usage with cost
- **WHEN** the first usage is `{"prompt_tokens":900,"completion_tokens":600,"total_tokens":1500,"cost":0.0012}` and the retry's is `{"prompt_tokens":950,"completion_tokens":300,"total_tokens":1250,"cost":0.0010}`
- **THEN** `usage` is `{"prompt_tokens":1850,"completion_tokens":900,"total_tokens":2750,"cost":0.0022}`

#### Scenario: Nested and single-report members
- **WHEN** the reports have `prompt_tokens_details` objects with overlapping and distinct members, and members present in only one report
- **THEN** overlapping nested numbers are summed and every other member is kept

#### Scenario: Only the retry reported usage
- **WHEN** the first reply had no `usage` and the retry reported `{"total_tokens":400}`
- **THEN** `usage` is `{"total_tokens":400}`

### Requirement: LLM reply retry logging
Each reply retry SHALL be logged with the analyzer id, the retry reason and the model used for the retry, and never with request or reply content.

#### Scenario: Retry logged
- **WHEN** a reply retry is sent
- **THEN** the log has `analyzer_id`, `reason` and `model` and no reply content

### Requirement: LLM reply retry tests
Every scenario of the shared `llm-reply-retry` delta and of this change SHALL have a Go test that uses stub providers only, never a real LLM provider.

#### Scenario: Stub-only tests
- **WHEN** `go test ./...` runs
- **THEN** the reply retry tests pass against `httptest` stubs without network access to a provider
