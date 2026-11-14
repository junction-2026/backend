## MODIFIED Requirements

### Requirement: Execution settings
`execution` SHALL have `adapter` (`http_sync`, `http_async`, `propaganda_detection`, `mistral_translation`, `custom` or `llm`), `endpoint`, `method` (default `POST`), `headers`, `timeout` (default `30s`; see LLM settings), `poll_interval` (default `1s`, used by `http_async`) and `handler` (`custom` only). The adapter `propaganda_detection` SHALL use `endpoint`, `headers` and `timeout`; its `method` SHALL be absent or `POST`, and `handler` and `poll_interval` SHALL be rejected for it.

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
Loading SHALL reject a missing or empty `endpoint` for the http adapters and for `propaganda_detection`, `input.bindings` on a `propaganda_detection` analyzer other than the `text` binding, a `custom` analyzer whose `handler` is not a registered handler name, unknown adapters and triggers, non-positive durations and invalid output schemas. The errors SHALL name the file and the analyzer id, and `${VAR}` expansion of endpoint and header values applies to `propaganda_detection` unchanged.

#### Scenario: Unknown custom handler
- **WHEN** a `custom` analyzer names the handler `nope`
- **THEN** loading fails with an error naming the handler

#### Scenario: Missing endpoint
- **WHEN** an `http_sync` or `propaganda_detection` analyzer has no endpoint or an empty one
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: Bindings on propaganda detection
- **WHEN** a `propaganda_detection` analyzer binds a name other than `text`, or `text` to `$input.source`, a `$job.*` source or an `$analyzers.<id>` whose id is not in `depends_on`
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: Propaganda detection placeholders
- **WHEN** an enabled `propaganda_detection` analyzer has `endpoint: ${PROPAGANDA_DETECTION_URL}`
- **THEN** it loads when the variable is set, loading fails naming the variable when it is unset, and a disabled analyzer with the unset variable loads

#### Scenario: Invalid output schema
- **WHEN** an analyzer declares `output.schema` `{type: 12}`
- **THEN** loading fails

### Requirement: Propaganda detection example
`docs/examples/propaganda_detection.yaml` SHALL define the analyzer `translation` (name `Translation to English`, category `translation`, adapter `mistral_translation`, `api_key: ${MISTRAL_API_KEY}`, model `mistral-small-latest`, timeout `60s`) and the analyzer `propaganda_detection` (adapter `propaganda_detection`, endpoint `${PROPAGANDA_DETECTION_URL}`, timeout `30s`, `depends_on: [translation]`, `input.bindings: {text: "$analyzers.translation"}`) with its `llm` opt-in block.

#### Scenario: Example loads
- **WHEN** the example is loaded with `MISTRAL_API_KEY` and `PROPAGANDA_DETECTION_URL` set
- **THEN** analyzers `translation` and `propaganda_detection` are loaded and enabled, the latter depending on the former with the `text` binding and keeping its `llm` block

#### Scenario: Documentation and sample registry
- **WHEN** the README is read
- **THEN** it documents `MISTRAL_API_KEY`, `PROPAGANDA_DETECTION_URL`, the model, timeout, `max_input_chars`, truncation, the cost and latency of the extra call and the dependency, and how to add the example to `ANALYZERS_CONFIG`, while `config/analyzers` contains neither analyzer (an enabled analyzer with an unset variable aborts startup)

## ADDED Requirements

### Requirement: Propaganda detection text binding
A `propaganda_detection` analyzer MAY have `input.bindings` with exactly one member `text` whose source is `$input` (the default behaviour) or `$analyzers.<id>` with `<id>` in `depends_on`. Any other binding name or source SHALL be rejected at load with an error naming the file and the analyzer id.

#### Scenario: Binding to a translation
- **WHEN** `propaganda_detection` has `depends_on: [translation]` and `input.bindings: {text: "$analyzers.translation"}`
- **THEN** it loads

#### Scenario: Other binding
- **WHEN** it binds `other: $input`, `text: $input.source`, `text: $job.id` or `text: $analyzers.x` with `x` not in `depends_on`
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: No bindings
- **WHEN** a `propaganda_detection` analyzer has no `input.bindings`
- **THEN** it loads and behaves as before

### Requirement: Mistral translation settings
The `mistral_translation` adapter SHALL take `endpoint` (default `https://api.mistral.ai/v1/chat/completions`), `api_key` (required), `model` (default `mistral-small-latest`), `timeout` (default `60s`), `max_input_chars` (positive integer, default `20000`) and `headers`. `method`, `handler`, `poll_interval` and `input.bindings` SHALL be rejected for it.

#### Scenario: Mistral translation defaults
- **WHEN** a `mistral_translation` execution only sets `api_key`
- **THEN** it loads with endpoint `https://api.mistral.ai/v1/chat/completions`, model `mistral-small-latest`, timeout 60s and `max_input_chars` 20000

#### Scenario: Mistral translation rejected settings
- **WHEN** a `mistral_translation` execution sets `method`, `handler` or `poll_interval`, has no `api_key`, has `input.bindings`, or sets `max_input_chars` to 0 or a negative number
- **THEN** loading fails with an error naming the file and analyzer id

### Requirement: Mistral translation placeholders
For an enabled `mistral_translation` analyzer `${VAR}` expansion SHALL apply to `endpoint`, `api_key`, `model` and `headers`. An unset variable SHALL abort loading with an error naming the variable and never a value; a disabled analyzer is not expanded.

#### Scenario: Unset key variable
- **WHEN** an enabled `mistral_translation` analyzer has `api_key: ${MISTRAL_API_KEY}`
- **THEN** it loads when the variable is set, loading fails naming the variable when it is unset, and a disabled analyzer with the unset variable loads
