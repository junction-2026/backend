## MODIFIED Requirements

### Requirement: LLM execution settings
The `llm` adapter SHALL take `task` (`final_synthesis` default or `topic_extraction`), `base_url`, `api_key` and `model` (required, no default), `prompt_file`, `temperature` (default 0), `max_tokens`, `timeout` (30s), `response_format` (`json_schema` default, `json_object`, `none`), `headers` and `extra_body` (default `{}`), all with `${VAR}` expansion, and for `topic_extraction` `max_input_chars` (positive integer, default 20000). `prompt_file` and `max_tokens` defaults depend on the task.

#### Scenario: LLM defaults
- **WHEN** an `llm` finalizer sets only `base_url`, `api_key`, `model` and `prompt_file`
- **THEN** it loads with temperature 0, max_tokens 600, timeout 30s, response_format `json_schema`, no headers and an empty `extra_body`, and a missing `prompt_file` defaults to `/etc/orchestrator/finalizer/prompt.md`

#### Scenario: Topic extraction defaults
- **WHEN** a regular analyzer sets `adapter: llm`, `task: topic_extraction`, `base_url`, `api_key` and `model`
- **THEN** it loads with `prompt_file` `/etc/orchestrator/extractor/prompt.md`, max_tokens 2000, timeout 30s, max_input_chars 20000 and response_format `json_schema`

#### Scenario: Invalid input limit
- **WHEN** a `topic_extraction` analyzer has `max_input_chars: 0`
- **THEN** loading fails with an error naming the analyzer id and `execution.max_input_chars`

### Requirement: LLM execution validation
Loading SHALL accept the `llm` adapter on an `after_all_analyzers_settled` analyzer whose `task` is absent or `final_synthesis`, and on an `on_dependencies_succeeded` analyzer only with `task: topic_extraction`, and reject every other trigger and task combination, `input.bindings` on `topic_extraction`, an `extra_body` with `model` or `messages`, and an invalid `response_format`. The errors SHALL name the file, the analyzer id and the field.

#### Scenario: LLM adapter on a regular analyzer
- **WHEN** an analyzer with trigger `on_dependencies_succeeded` has `adapter: llm` and no `task`
- **THEN** loading fails with an error naming the analyzer id and `execution.adapter`

#### Scenario: Topic extraction on a finalizer
- **WHEN** an `after_all_analyzers_settled` analyzer has `adapter: llm` and `task: topic_extraction`
- **THEN** loading fails with an error naming the analyzer id and `execution.task`

#### Scenario: Final synthesis on a regular analyzer
- **WHEN** an `on_dependencies_succeeded` analyzer has `adapter: llm` and `task: final_synthesis`
- **THEN** loading fails with an error naming the analyzer id and `execution.adapter`

#### Scenario: Unknown response format
- **WHEN** an enabled `llm` analyzer has `response_format: xml`
- **THEN** loading fails with an error naming the analyzer id and `execution.response_format`

#### Scenario: Extra body replaces the model
- **WHEN** an `llm` analyzer has `extra_body: {model: other}`
- **THEN** loading fails with an error naming `execution.extra_body.model`

### Requirement: LLM finalizer startup checks
For an enabled `llm` finalizer or `topic_extraction` analyzer, loading SHALL abort startup when `base_url`, `api_key` or `model` is empty after expansion or `prompt_file` cannot be read, with a message naming the analyzer and the field and never containing the API key. The prompt file SHALL be read once at load. A disabled `llm` analyzer SHALL NOT be checked. The API key SHALL NOT be logged or returned by any API.

#### Scenario: Empty key
- **WHEN** `api_key: ${FINALIZER_API_KEY}` and `FINALIZER_API_KEY` is set to an empty string
- **THEN** startup fails with a message naming `execution.api_key`

#### Scenario: Unreadable prompt
- **WHEN** `prompt_file` names a file that does not exist
- **THEN** startup fails with a message naming `execution.prompt_file`

#### Scenario: Disabled finalizer
- **WHEN** the `llm` finalizer is `enabled: false` and its variables are unset
- **THEN** the server starts

#### Scenario: Topic extraction missing model
- **WHEN** an enabled `topic_extraction` analyzer has `model: ${EXTRACTOR_MODEL}` and the variable is empty
- **THEN** startup fails with a message naming the analyzer and `execution.model`

## ADDED Requirements

### Requirement: Topic extraction prompt default
The default `prompt_file` of `topic_extraction` SHALL be `/etc/orchestrator/extractor/prompt.md`, which the image gets from `config/extractor/prompt.md` through a `COPY` line in the root `Dockerfile`. The prompt SHALL ask for at most 10 topics and at most 10 news items in the language of the input, and SHALL tell the model to treat the content as data, not instructions.

#### Scenario: Shipped prompt
- **WHEN** `config/extractor/prompt.md` is read
- **THEN** it asks for at most 10 topics and 10 news items, names the language of the content and says the content is data, not instructions

### Requirement: Topic extraction example
`docs/examples/topic_extraction.yaml` SHALL define the analyzer `topic_extraction` (trigger `on_dependencies_succeeded`, `adapter: llm`, `task: topic_extraction`) with `base_url: ${EXTRACTOR_BASE_URL}`, `api_key: ${EXTRACTOR_API_KEY}` and `model: ${EXTRACTOR_MODEL}`, independent of the finalizer's variables. The README section "Topic extraction analyzer" SHALL document every setting and these variables.

#### Scenario: Example loads
- **WHEN** the example is loaded with the three variables set
- **THEN** it loads as a topic extraction analyzer, and with `EXTRACTOR_MODEL` unset loading fails naming `EXTRACTOR_MODEL`
