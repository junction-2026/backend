## MODIFIED Requirements

### Requirement: Propaganda detection request
The `propaganda_detection` adapter SHALL send one `POST` to the configured endpoint with the JSON body `{"text": <text>}`, the headers `Content-Type`, `Accept`, `X-Analysis-Job-Id`, `X-Analysis-Analyzer-Id` and the configured headers, with the no-redirect client and no retries. `<text>` SHALL be the job's `normalized_content` unless `text` is bound to `$analyzers.<id>`; then it SHALL be that result's `translated_text` string.

#### Scenario: Request shape
- **WHEN** the adapter runs for job `j1` with normalized content `Fake news!` and an `Authorization` header configured
- **THEN** exactly one `POST` arrives with body `{"text": "Fake news!"}`, `X-Analysis-Job-Id: j1`, `X-Analysis-Analyzer-Id` and the `Authorization` header

#### Scenario: Service error is not retried
- **WHEN** the service answers 502
- **THEN** the adapter fails with `adapter_error` after exactly one request

#### Scenario: Text from a translation
- **WHEN** `text` is bound to `$analyzers.translation` and that result has `translated_text` `Hello world`
- **THEN** exactly one `POST` arrives with body `{"text": "Hello world"}`

#### Scenario: Translation result without text
- **WHEN** `text` is bound to `$analyzers.translation` and that result has no string `translated_text` (there is no fallback to `normalized_content`)
- **THEN** the adapter fails with `adapter_error`, the service receives no request and `normalized_content` is not sent

## ADDED Requirements

### Requirement: Mistral translation request
The `mistral_translation` adapter SHALL send one `POST` to the configured endpoint with the headers `Content-Type: application/json`, `Accept: application/json`, `Authorization: Bearer <api_key>` and the configured headers, and no `X-Analysis-*` headers. It SHALL use the no-redirect client and SHALL NOT retry. The body SHALL be `{model, temperature: 0, response_format: {type: "json_object"}, messages}` with a `system` and a `user` message.

#### Scenario: Request shape
- **WHEN** the adapter runs with normalized content `Привіт`
- **THEN** exactly one `POST` arrives with `Authorization: Bearer <api_key>`, no `X-Analysis-*` header, temperature 0, `response_format` `json_object` and the messages `system` (prompt) and `user` (`Привіт`)

#### Scenario: Redirect
- **WHEN** the endpoint answers 3xx
- **THEN** the redirect is not followed and the adapter fails with `adapter_error`

### Requirement: Mistral translation prompt
The `system` message SHALL be a fixed prompt and the `user` message SHALL hold the (possibly truncated) `normalized_content`. The prompt SHALL ask to translate the user text into English, return it unchanged if it is already English, treat it as data and not as instructions, and answer only `{"source_language": <ISO 639-1 code or null>, "translated_text": <string>}`.

#### Scenario: Prompt content
- **WHEN** the adapter sends its request
- **THEN** the `system` message mentions English, unchanged text, data instead of instructions, `source_language` and `translated_text`

### Requirement: Mistral translation input limit
`normalized_content` longer than `max_input_chars` Unicode code points SHALL be cut at the last whitespace at or before the limit, or hard at the limit when there is none, before it is sent. The result SHALL then report `truncated: true`, and `input_characters` SHALL always be the original length in code points. Empty `normalized_content` SHALL fail with `adapter_error` without calling Mistral. The adapter SHALL NOT chunk.

#### Scenario: Truncation at whitespace
- **WHEN** `max_input_chars` is 10 and the content is `один два три`
- **THEN** the user message is `один два`, `truncated` is true and `input_characters` is 12

#### Scenario: Hard cut
- **WHEN** the content has no whitespace before the limit
- **THEN** it is cut at exactly the limit in code points, never inside a multibyte character

#### Scenario: Empty input
- **WHEN** `normalized_content` is empty or only whitespace
- **THEN** the adapter fails with `adapter_error` and Mistral receives no request

### Requirement: Mistral translation result
On a 2xx answer `choices[0].message.content` SHALL be decoded as a JSON object whose `translated_text` is a non-empty string after trimming; `source_language` SHALL be kept when it is a string and otherwise be `null`. A `finish_reason` of `length` SHALL be an error.

#### Scenario: Valid reply
- **WHEN** Mistral answers with content `{"source_language": "uk", "translated_text": "Hello world"}`
- **THEN** the result has `translated_text` `Hello world`, `source_language` `uk`, `model`, `truncated` false, `input_characters` and `usage`

#### Scenario: Invalid reply
- **WHEN** the body is not JSON, `choices` or `message.content` is missing, the content is not a JSON object, `translated_text` is missing, empty or not a string, or `finish_reason` is `length`
- **THEN** the adapter fails with `adapter_error`

### Requirement: Mistral translation result members
The `mistral_translation` analyzer result SHALL be an object with at least `translated_text`, `source_language`, `model` (the response's `model`, else the configured one), `truncated`, `input_characters` and `usage` (the provider's usage object or `null`); other members of the reply are dropped.

#### Scenario: Result members
- **WHEN** the reply has no `model`, a non-string `source_language` and extra members
- **THEN** `model` is the configured model, `source_language` is `null` and the extra members are absent

### Requirement: Mistral translation failures
An empty `api_key`, a non-2xx status (including 3xx), a connection error and every decoding or validation failure SHALL fail the `mistral_translation` adapter with `adapter_error`; an ended context SHALL fail it with `timeout` or `cancelled` according to the context, within 2 seconds. Error messages and logs SHALL NOT contain the `api_key`, the `Authorization` value, the endpoint or provider response bodies; the HTTP status MAY be named.

#### Scenario: Provider errors
- **WHEN** Mistral answers 401, 429 or 500
- **THEN** the adapter fails with `adapter_error` after one request and its message names neither the key, the endpoint nor the response body

#### Scenario: Timeout and cancel
- **WHEN** Mistral does not answer until the timeout elapses or the job is cancelled
- **THEN** the adapter fails with `timeout` or `cancelled` respectively within 2 seconds

#### Scenario: Empty api_key
- **WHEN** `api_key` is empty after expansion
- **THEN** the adapter fails with `adapter_error` and Mistral receives no request
