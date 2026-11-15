## MODIFIED Requirements

### Requirement: LLM techniques settings
The registry SHALL accept adapter `llm_techniques` with execution settings `base_url`, `api_key`, `model` (required when enabled, `${VAR}` expanded), `prompt_file` (default `/etc/orchestrator/llm_techniques/prompt.md`), `temperature`, `max_tokens` (default 1500), `headers`, `min_confidence` (0..1, default 0.6), `max_findings_per_label` (positive, default 5), `max_input_chars` (positive, default 20000) and `timeout`.

#### Scenario: Defaults
- **WHEN** an `llm_techniques` analyzer sets only base_url, api_key and model
- **THEN** it loads with min_confidence 0.6, max_findings_per_label 5, max_input_chars 20000 and the image prompt file

#### Scenario: Out of range
- **WHEN** `min_confidence` is 1.5 or `max_findings_per_label` is 0
- **THEN** loading fails naming the analyzer id and the field

## ADDED Requirements

### Requirement: propaganda_detection llm description
The `llm.description` of `docs/examples/propaganda_detection.yaml` SHALL state that the analyzer is a coarse whole-text classifier whose `score` does not measure the strength of propaganda and that PROPAGANDA is supporting evidence only together with another strong signal.

#### Scenario: Description
- **WHEN** the example is loaded
- **THEN** its description contains those statements
