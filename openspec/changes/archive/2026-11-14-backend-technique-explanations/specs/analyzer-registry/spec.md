## ADDED Requirements

### Requirement: LLM techniques settings
The registry SHALL accept adapter `llm_techniques` with execution settings `base_url`, `api_key`, `model` (required when enabled, `${VAR}` expanded), `prompt_file` (default `/etc/orchestrator/llm_techniques/prompt.md`), `temperature`, `max_tokens` (default 1500), `headers`, `min_confidence` (0..1, default 0.5), `max_findings_per_label` (positive, default 5), `max_input_chars` (positive, default 20000) and `timeout`.

#### Scenario: Defaults
- **WHEN** an `llm_techniques` analyzer sets only base_url, api_key and model
- **THEN** it loads with min_confidence 0.5, max_findings_per_label 5, max_input_chars 20000 and the image prompt file

#### Scenario: Out of range
- **WHEN** `min_confidence` is 1.5 or `max_findings_per_label` is 0
- **THEN** loading fails naming the analyzer id and the field

### Requirement: LLM techniques settings scope
Any other execution key SHALL be rejected for `llm_techniques`; its `input.bindings` SHALL be exactly `translation: $analyzers.<id>` and its trigger `on_dependencies_succeeded`. `min_confidence` and `max_findings_per_label` SHALL be rejected on other adapters.

#### Scenario: Key of another adapter
- **WHEN** an `llm_techniques` analyzer sets `endpoint`, or a `technique_classifier` sets `min_confidence`
- **THEN** loading fails

### Requirement: LLM techniques example and finalizer description
`docs/examples/llm_techniques.yaml` SHALL register id `llm_techniques`, category `propaganda`, `depends_on: [translation]` and `llm.include: true` with a description saying its findings are LLM-assessed, weaker evidence than the `techniques` model labels and must not be treated as model detections. The image SHALL ship the prompt at its default path.

#### Scenario: Finalizer input
- **WHEN** the example is loaded with a final_synthesis finalizer and `llm_techniques` succeeds
- **THEN** the finalizer's entry for `llm_techniques` carries that description
