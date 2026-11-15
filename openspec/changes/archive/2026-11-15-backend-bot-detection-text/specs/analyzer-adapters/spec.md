## ADDED Requirements

### Requirement: Bot detection request
The `bot_detection` adapter SHALL send exactly one POST to `execution.endpoint` with the JSON body `{"text": <input.normalized_content>}`, `Content-Type: application/json` and the configured headers. The text SHALL come from the `content` binding (`$input`, the default) and never from an `$analyzers.*` binding, so it is the original text and never the translation. An empty or missing `normalized_content` SHALL fail with `adapter_error` and send no request.

#### Scenario: Translated job
- **WHEN** a Russian job has a succeeded `translation`
- **THEN** the detector receives the original `normalized_content` and not the `translated_text`

#### Scenario: Translation failed
- **WHEN** `translation` failed
- **THEN** `bot_detection` still runs

#### Scenario: No text
- **WHEN** `normalized_content` is empty
- **THEN** the run fails with `adapter_error` and the detector receives no request

### Requirement: Bot detection response forms
The adapter SHALL accept the answers `{"label","score"}`, `[{"label","score"},...]`, `[[{"label","score"},...]]` and `{"result": <any of these>}`. When several elements exist, the element with the highest score SHALL be the top label. A `latency_ms` member SHALL be ignored.

#### Scenario: List answer
- **WHEN** the detector answers `[{"label":"LABEL_0","score":0.7},{"label":"LABEL_1","score":0.3}]`
- **THEN** the top label is `LABEL_0` with 0.7

### Requirement: Bot detection label mapping
`LABEL_1` (bot / generated) with probability p SHALL give `label: "bot_or_generated"` and `score: p`; `LABEL_0` (human) with p SHALL give `label: "human"` and `score: 1 - p`. When both labels are listed, `score` SHALL be the `LABEL_1` probability. Any other label SHALL fail with `invalid_output`. The result SHALL have at least `label`, `score`, `scope: "text"`, `model_label` and `model_score` (the raw top label and its probability).

#### Scenario: Bot label
- **WHEN** the detector answers `{"label":"LABEL_1","score":0.87}`
- **THEN** the result is `{"label":"bot_or_generated","score":0.87,"scope":"text","model_label":"LABEL_1","model_score":0.87}`

#### Scenario: Human label at the bounds
- **WHEN** the detector answers `LABEL_0` with 0, or `LABEL_0` with 1
- **THEN** `score` is 1, or 0

### Requirement: Bot detection failures
The adapter SHALL fail with `invalid_output` for an unknown label, a missing or non-numeric score, a score outside 0..1, a non-object or empty answer; with `adapter_error` for a non-2xx answer (message holds the HTTP status only), a connection error or empty text; with `timeout` when the timeout elapses. `error.message` SHALL NOT contain a configured header value or token, even when the service echoes it.

#### Scenario: Echoed token
- **WHEN** the service answers HTTP 401 with a body repeating the bearer token
- **THEN** `error.message` contains the status and not the token
