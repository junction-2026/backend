## MODIFIED Requirements

### Requirement: Execution settings
`execution` SHALL have `adapter` (`http_sync`, `http_async`, `propaganda_detection`, `mistral_translation` or `custom`), `endpoint`, `method` (default `POST`), `headers`, `timeout` (default `30s`), `poll_interval` (default `1s`) and `handler` (`custom` only). Both `propaganda_detection` and `mistral_translation` SHALL reject `handler` and `poll_interval`. `mistral_translation` also takes `api_key` (required), `model` and `max_input_chars` and no `method`.

#### Scenario: Execution defaults
- **WHEN** an `http_async` execution only sets an endpoint
- **THEN** method is `POST`, timeout 30s and poll interval 1s

#### Scenario: Propaganda detection settings
- **WHEN** a `propaganda_detection` execution sets an endpoint and headers and omits `method` and `timeout`
- **THEN** it loads with method `POST` and timeout 30s

#### Scenario: Propaganda detection rejected settings
- **WHEN** a `propaganda_detection` execution sets `method: GET`, a `handler` or a `poll_interval`
- **THEN** loading fails with an error naming the file and analyzer id

#### Scenario: Mistral translation defaults
- **WHEN** a `mistral_translation` execution only sets `api_key`
- **THEN** it loads with endpoint `https://api.mistral.ai/v1/chat/completions`, model `mistral-small-latest`, timeout 60s and `max_input_chars` 20000

#### Scenario: Mistral translation rejected settings
- **WHEN** a `mistral_translation` execution sets `method`, `handler` or `poll_interval`, has no `api_key`, or sets `max_input_chars` to 0 or a negative number
- **THEN** loading fails with an error naming the file and analyzer id

### Requirement: Execution validation
Loading SHALL reject a missing `endpoint` (except for `mistral_translation`), an unregistered `custom` handler, unknown adapters and triggers, non-positive durations, invalid output schemas and the `input.bindings` rejected below, naming the file and the analyzer id. `${VAR}` expansion also applies to `api_key` and `model` of `mistral_translation`; an unset variable of an enabled analyzer aborts loading naming it.

#### Scenario: Unknown custom handler
- **WHEN** a `custom` analyzer names the handler `nope`
- **THEN** loading fails with an error naming the handler

#### Scenario: Missing endpoint
- **WHEN** an `http_sync` or `propaganda_detection` analyzer has no endpoint or an empty one
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: Bindings on propaganda detection
- **WHEN** a `propaganda_detection` analyzer binds a name other than `text`, or `text` to `$input.source`, a `$job.*` source or an `$analyzers.<id>` whose id is not in `depends_on`
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: Bindings on mistral translation
- **WHEN** a `mistral_translation` analyzer has `input.bindings`
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: Propaganda detection placeholders
- **WHEN** an enabled `propaganda_detection` analyzer has `endpoint: ${PROPAGANDA_DETECTION_URL}`
- **THEN** it loads when the variable is set, loading fails naming the variable when it is unset, and a disabled analyzer with the unset variable loads

#### Scenario: Mistral translation placeholders
- **WHEN** an enabled `mistral_translation` analyzer has `api_key: ${MISTRAL_API_KEY}`
- **THEN** it loads when the variable is set, loading fails naming the variable when it is unset, and a disabled analyzer with the unset variable loads

#### Scenario: Invalid output schema
- **WHEN** an analyzer declares `output.schema` `{type: 12}`
- **THEN** loading fails

### Requirement: Propaganda detection example
`docs/examples/propaganda_detection.yaml` SHALL define the analyzer `translation` (name `Translation to English`, category `translation`, adapter `mistral_translation`, `api_key: ${MISTRAL_API_KEY}`, model `mistral-small-latest`, timeout `60s`) and the analyzer `propaganda_detection` (adapter `propaganda_detection`, endpoint `${PROPAGANDA_DETECTION_URL}`, timeout `30s`) with `depends_on: [translation]` and `input.bindings: {text: "$analyzers.translation"}`.

#### Scenario: Example loads
- **WHEN** the example is loaded with `MISTRAL_API_KEY` and `PROPAGANDA_DETECTION_URL` set
- **THEN** analyzers `translation` and `propaganda_detection` are loaded and enabled, the latter depending on the former with the `text` binding

#### Scenario: Documentation and sample registry
- **WHEN** the README is read
- **THEN** it documents `MISTRAL_API_KEY`, `PROPAGANDA_DETECTION_URL`, the model, timeout, `max_input_chars`, truncation, the cost and latency of the extra call and the dependency, and how to add the example to `ANALYZERS_CONFIG`, while `config/analyzers` contains neither analyzer (an enabled analyzer with an unset variable aborts startup)
