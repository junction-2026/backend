## MODIFIED Requirements

### Requirement: Execution settings
`execution` SHALL have `adapter` (`http_sync`, `http_async`, `propaganda_detection`, `custom` or `llm`), `endpoint`, `method` (default `POST`), `headers` (map), `timeout` (duration, default `30s`; see LLM settings), `poll_interval` (duration, default `1s`, used by `http_async`) and `handler` (`custom` only). The adapter `propaganda_detection` SHALL use `endpoint`, `headers` and `timeout`; its `method` SHALL be absent or `POST`, and `handler` and `poll_interval` SHALL be rejected for it.

#### Scenario: Execution defaults
- **WHEN** an `http_async` execution only sets an endpoint
- **THEN** method is `POST`, timeout 30s and poll interval 1s

#### Scenario: Propaganda detection settings
- **WHEN** a `propaganda_detection` execution sets an endpoint and headers and omits `method` and `timeout`
- **THEN** it loads with method `POST` and timeout 30s

#### Scenario: Propaganda detection rejected settings
- **WHEN** a `propaganda_detection` execution sets `method: GET`, a `handler` or a `poll_interval`
- **THEN** loading fails with an error naming the file and analyzer id

### Requirement: Environment placeholders
`${VAR}` placeholders in `execution.endpoint`, in `execution.headers` values and in every string of the `llm` settings (including inside `extra_body`) SHALL be expanded from the process environment once at load time. A variable that is not set and is referenced by an enabled analyzer SHALL abort startup with an error naming the variable and the analyzer; disabled analyzers are not expanded. Expanded values SHALL NOT be logged and no error message SHALL contain one.

#### Scenario: Expansion
- **WHEN** an enabled analyzer has `endpoint: ${BASE}/sentiment` and `BASE=http://svc`
- **THEN** its endpoint is `http://svc/sentiment`

#### Scenario: Unset variable
- **WHEN** an enabled analyzer references `${NOPE}` and `NOPE` is not set
- **THEN** startup aborts with an error naming `NOPE` and the analyzer

#### Scenario: Disabled analyzer
- **WHEN** a disabled analyzer references an unset variable
- **THEN** loading succeeds

#### Scenario: LLM values
- **WHEN** an enabled `llm` finalizer has `headers: {X-Title: "${V}"}` and `extra_body: {provider: {order: ["${V}"]}}` with `V=x`
- **THEN** the header value is `x` and `extra_body.provider.order` is `["x"]`

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

### Requirement: LLM execution settings
The `llm` adapter SHALL take `base_url`, `api_key` and `model` (required, no default), `prompt_file` (default `/etc/orchestrator/finalizer/prompt.md`), `temperature` (default 0), `max_tokens` (default 600), `timeout` (default 30s), `response_format` (`json_schema`, `json_object` or `none`, default `json_schema`), `headers` (map, default `{}`) and `extra_body` (object, default `{}`).

#### Scenario: LLM defaults
- **WHEN** an `llm` finalizer sets only `base_url`, `api_key`, `model` and `prompt_file`
- **THEN** it loads with temperature 0, max_tokens 600, timeout 30s, response_format `json_schema`, no headers and an empty `extra_body`

### Requirement: LLM execution validation
Loading SHALL reject the `llm` adapter on an analyzer whose trigger is not `after_all_analyzers_settled`, an `extra_body` with the key `model` or `messages`, and for an enabled `llm` finalizer a `response_format` other than `json_schema`, `json_object` or `none`. The errors SHALL name the file, the analyzer id and the field.

#### Scenario: LLM adapter on a regular analyzer
- **WHEN** an analyzer with trigger `on_dependencies_succeeded` has `adapter: llm`
- **THEN** loading fails with an error naming the analyzer id and `execution.adapter`

#### Scenario: Unknown response format
- **WHEN** an enabled `llm` finalizer has `response_format: xml`
- **THEN** loading fails with an error naming the analyzer id and `execution.response_format`

#### Scenario: Extra body replaces the model
- **WHEN** an `llm` finalizer has `extra_body: {model: other}`
- **THEN** loading fails with an error naming `execution.extra_body.model`

### Requirement: LLM finalizer startup checks
For an enabled `llm` finalizer, loading SHALL abort startup when `base_url`, `api_key` or `model` is empty after expansion or `prompt_file` cannot be read, with a message naming the analyzer and the field and never containing the API key. The prompt file SHALL be read once at load. A disabled `llm` finalizer SHALL NOT be checked.

#### Scenario: Empty key
- **WHEN** `api_key: ${FINALIZER_API_KEY}` and `FINALIZER_API_KEY` is set to an empty string
- **THEN** startup fails with a message naming `execution.api_key`

#### Scenario: Unreadable prompt
- **WHEN** `prompt_file` names a file that does not exist
- **THEN** startup fails with a message naming `execution.prompt_file`

#### Scenario: Disabled finalizer
- **WHEN** the `llm` finalizer is `enabled: false` and its variables are unset
- **THEN** the server starts

### Requirement: Analyzer LLM input block
An analyzer MAY have `llm: {include: bool (default false), description: string}`. `include: true` with a description that is empty after trimming SHALL abort startup, and the block on an `after_all_analyzers_settled` analyzer SHALL be rejected; both errors name the analyzer and the field.

#### Scenario: Opted-in analyzer
- **WHEN** `propaganda_detection` has `llm: {include: true, description: "Binary classifier ..."}`
- **THEN** it loads and its runs are sent to the `llm` finalizer with that description

#### Scenario: Missing description
- **WHEN** an analyzer has `llm: {include: true}`
- **THEN** loading fails naming `llm.description`

#### Scenario: Block on the finalizer
- **WHEN** the finalizer itself has an `llm` block
- **THEN** loading fails

### Requirement: LLM finalizer example
`docs/examples/final_synthesis.yaml` SHALL be the `llm` finalizer entry with `base_url: ${FINALIZER_BASE_URL}`, `api_key: ${FINALIZER_API_KEY}` and `model: ${FINALIZER_MODEL}`, and `docs/examples/propaganda_detection.yaml` SHALL have `llm: {include: true, description: ...}` with the description from the parent issue. The README SHALL document these variables and the `response_format` modes.

#### Scenario: Examples run
- **WHEN** both examples and `text_stats` are loaded with the variables pointing at stubs
- **THEN** a job ends `completed` with a `detected` result and the LLM request contains `propaganda_detection` with its description and no `text_stats`
