## ADDED Requirements

### Requirement: Technique span settings
The registry SHALL accept an optional `spans` block on `execution.adapter: technique_classifier` with `sentence_model`, `llm`, `timeout` (the span deadline), `max_sentences` (default 20), `max_phrases_per_technique` (default 5) and `max_input_chars` (default 20000). The block SHALL NOT be accepted on other adapters.

#### Scenario: Default caps
- **WHEN** a `spans` block with `timeout`, `sentence_model` and `llm` only is loaded
- **THEN** `max_sentences` is 20, `max_phrases_per_technique` is 5 and `max_input_chars` is 20000

#### Scenario: Spans on another adapter
- **WHEN** an `http_sync` analyzer sets `spans`
- **THEN** loading fails

### Requirement: Technique span sentence model settings
`spans.sentence_model` SHALL take `endpoint`, `headers`, `threshold` (required, 0 to 1) and `thresholds` (per-technique overrides), with `${VAR}` expansion of the endpoint and header values like the `short` and `long` blocks.

#### Scenario: Override map
- **WHEN** `spans.sentence_model` has `threshold: 0.4` and `thresholds: {Doubt: 0.6}`
- **THEN** the effective threshold of `Doubt` is 0.6 and of every other technique 0.4

### Requirement: Technique span LLM settings
`spans.llm` SHALL take `base_url`, `api_key`, `model`, `prompt_file` (default `/etc/orchestrator/technique_spans/prompt.md`), `temperature`, `max_tokens` (default 1000), `response_format` (default `json_schema`), `headers` and `extra_body`, with `${VAR}` expansion as for the llm adapter. The prompt file SHALL be read once at load.

#### Scenario: Expansion
- **WHEN** an enabled analyzer sets `spans.llm` with `${VAR}` values and the variables are set
- **THEN** `base_url`, `api_key`, `model` and header values are expanded and the prompt is loaded

### Requirement: Technique span validation
Loading SHALL fail with an error naming the file, the analyzer id and the field when `spans` lacks `sentence_model`, `llm` or a positive `timeout`, when `spans.timeout` is not shorter than `execution.timeout`, when the sentence model block is invalid like a `short` block would be, when a cap is not a positive integer or when `llm.extra_body` sets `model` or `messages`.

#### Scenario: Span deadline too long
- **WHEN** `execution.timeout` is 30s and `spans.timeout` is 30s
- **THEN** loading fails naming `execution.spans.timeout`

#### Scenario: Unknown technique override
- **WHEN** `spans.sentence_model.thresholds` has the key `Nope`
- **THEN** loading fails

### Requirement: Technique span LLM loading
For an enabled analyzer, loading SHALL fail, naming the field and never a value, when a variable in `spans` is unset, `spans.llm.base_url`, `api_key` or `model` is empty, `response_format` is unknown or the prompt file cannot be read.

#### Scenario: Empty api key
- **WHEN** an enabled analyzer's `spans.llm.api_key` expands to an empty string
- **THEN** loading fails naming `execution.spans.llm.api_key`

## MODIFIED Requirements

### Requirement: Technique classifier example
`docs/examples/technique_classifier.yaml` SHALL define the analyzer `techniques` (category `propaganda`, `depends_on: [translation]`, binding `text: "$analyzers.translation"`, endpoints `${TECHNIQUE_SHORT_URL}` and `${TECHNIQUE_LONG_URL}`, header `Authorization: Bearer ${ML_API_TOKEN}`, thresholds 0.3 and 0.5, `llm.include: true` with a description that mentions spans) and a `spans` block. The README SHALL document it in its own section with its variables.

#### Scenario: Example loads
- **WHEN** the example is loaded next to an analyzer `translation` with its variables set
- **THEN** `techniques` is enabled, depends on `translation`, keeps its `llm` block and has a loaded `spans` block

#### Scenario: Span variables
- **WHEN** the example's `spans` block is read
- **THEN** it uses `${TECHNIQUE_SPAN_URL}`, `${SPAN_LLM_BASE_URL}`, `${SPAN_LLM_API_KEY}` and `${SPAN_LLM_MODEL}`

#### Scenario: Span variable missing
- **WHEN** the example is loaded without `SPAN_LLM_API_KEY`
- **THEN** loading fails naming `SPAN_LLM_API_KEY`
