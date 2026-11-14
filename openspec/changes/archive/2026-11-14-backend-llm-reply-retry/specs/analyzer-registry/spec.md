## ADDED Requirements

### Requirement: LLM reply retry settings
An `llm` execution SHALL also take `retry_invalid_output` (boolean, default true), `retry_max_tokens` (positive integer, default twice `max_tokens`), `retry_model` (string with `${VAR}` expansion, default empty meaning the same model) and `retry_min_remaining` (duration, at least 0, default 5s).

#### Scenario: Retry defaults
- **WHEN** an `llm` finalizer sets `max_tokens: 700` and no retry setting
- **THEN** reply retry is enabled with retry_max_tokens 1400, the same model and retry_min_remaining 5s

#### Scenario: Retry settings set
- **WHEN** an `llm` finalizer sets `retry_invalid_output: false`, `retry_max_tokens: 2500`, `retry_model: ${V}-big` with `V=x` and `retry_min_remaining: 0s`
- **THEN** it loads with reply retry disabled, retry_max_tokens 2500, retry model `x-big` and retry_min_remaining 0


### Requirement: LLM reply retry settings validation
Loading SHALL fail with an error naming the analyzer id and the field when an `llm` execution has `retry_max_tokens` below 1 (`execution.retry_max_tokens must be a positive integer`) or a negative `retry_min_remaining` (`execution.retry_min_remaining must not be negative`). A value of the wrong YAML type for any retry setting SHALL fail loading as a decode error.

#### Scenario: Non-positive retry_max_tokens
- **WHEN** an `llm` finalizer has `retry_max_tokens: 0` or `retry_max_tokens: -5`
- **THEN** loading fails with an error naming the analyzer id and `execution.retry_max_tokens`

#### Scenario: Negative retry_min_remaining
- **WHEN** an `llm` finalizer has `retry_min_remaining: -1s`
- **THEN** loading fails with an error naming the analyzer id and `execution.retry_min_remaining`

#### Scenario: Wrong type
- **WHEN** an `llm` finalizer has `retry_min_remaining: soon` or `retry_invalid_output: maybe`
- **THEN** loading fails
