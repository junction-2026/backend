## MODIFIED Requirements

### Requirement: Execution settings
`execution` SHALL have `adapter` (`http_sync`, `http_async`, `propaganda_detection` or `custom`), `endpoint`, `method` (default `POST`), `headers` (map), `timeout` (duration, default `30s`), `poll_interval` (duration, default `1s`, used by `http_async`) and `handler` (`custom` only). The adapter `propaganda_detection` SHALL use `endpoint`, `headers` and `timeout`; its `method` SHALL be absent or `POST`, and `handler` and `poll_interval` SHALL be rejected for it.

#### Scenario: Execution defaults
- **WHEN** an `http_async` execution only sets an endpoint
- **THEN** method is `POST`, timeout 30s and poll interval 1s

#### Scenario: Propaganda detection settings
- **WHEN** a `propaganda_detection` execution sets an endpoint and headers and omits `method` and `timeout`
- **THEN** it loads with method `POST` and timeout 30s

#### Scenario: Propaganda detection rejected settings
- **WHEN** a `propaganda_detection` execution sets `method: GET`, a `handler` or a `poll_interval`
- **THEN** loading fails with an error naming the file and analyzer id

### Requirement: Execution validation
Loading SHALL reject a missing or empty `endpoint` for the http adapters and for `propaganda_detection`, `input.bindings` on a `propaganda_detection` analyzer (its text source is fixed), a `custom` analyzer whose `handler` is not a registered handler name, unknown adapters and triggers, non-positive durations and invalid output schemas. The errors SHALL name the file and the analyzer id, and `${VAR}` expansion of endpoint and header values applies to `propaganda_detection` unchanged.

#### Scenario: Unknown custom handler
- **WHEN** a `custom` analyzer names the handler `nope`
- **THEN** loading fails with an error naming the handler

#### Scenario: Missing endpoint
- **WHEN** an `http_sync` or `propaganda_detection` analyzer has no endpoint or an empty one
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: Bindings on propaganda detection
- **WHEN** a `propaganda_detection` analyzer has `input.bindings`
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: Propaganda detection placeholders
- **WHEN** an enabled `propaganda_detection` analyzer has `endpoint: ${PROPAGANDA_DETECTION_URL}`
- **THEN** it loads when the variable is set, loading fails naming the variable when it is unset, and a disabled analyzer with the unset variable loads

#### Scenario: Invalid output schema
- **WHEN** an analyzer declares `output.schema` `{type: 12}`
- **THEN** loading fails

## ADDED Requirements

### Requirement: Propaganda detection example
`docs/examples/propaganda_detection.yaml` SHALL define the analyzer `propaganda_detection` (name `Propaganda detection`, category `propaganda`, adapter `propaganda_detection`, endpoint `${PROPAGANDA_DETECTION_URL}`, timeout `30s`) with a commented optional `Authorization: Bearer ${PROPAGANDA_DETECTION_TOKEN}` header.

#### Scenario: Example loads
- **WHEN** the example is loaded with `PROPAGANDA_DETECTION_URL` set
- **THEN** analyzer `propaganda_detection` is loaded and enabled with the configured endpoint

#### Scenario: Documentation and sample registry
- **WHEN** the README is read
- **THEN** it documents `PROPAGANDA_DETECTION_URL` and how to add the example to `ANALYZERS_CONFIG`, while `config/analyzers` does not contain the analyzer (an enabled analyzer with an unset variable aborts startup)
