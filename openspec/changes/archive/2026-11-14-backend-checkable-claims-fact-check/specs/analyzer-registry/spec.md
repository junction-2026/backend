## ADDED Requirements

### Requirement: Fact check settings
Adapter `fact_check` SHALL accept the execution keys `base_url`, `api_key` (required when enabled), `min_check_worthiness`, `max_claims`, `page_size`, `max_age_days`, `max_results`, `max_concurrency`, `cache_ttl` and `timeout`, with the defaults of `docs/examples/fact_check.yaml` and `${VAR}` expansion in `base_url` and `api_key`. Loading SHALL fail, naming analyzer id and field, for an invalid value, a missing `api_key` or other `input.bindings`.

#### Scenario: Defaults
- **WHEN** an enabled `fact_check` entry sets only `api_key` and the binding
- **THEN** it loads with the defaults above

#### Scenario: Invalid value
- **WHEN** `min_check_worthiness` is 1.5, or `max_claims` is 0
- **THEN** loading fails with an error naming the analyzer id and the field

#### Scenario: Missing key
- **WHEN** an enabled entry has an empty `api_key`
- **THEN** loading fails naming `execution.api_key`, without any value

### Requirement: Topic extraction claim cap
The `topic_extraction` llm entry SHALL accept `execution.max_claims` (>=1, default 5); the key is rejected on every other adapter except `fact_check`.

#### Scenario: Invalid cap
- **WHEN** a `topic_extraction` entry has `max_claims: 0`
- **THEN** loading fails with an error naming the analyzer id and `execution.max_claims`

### Requirement: Fact check example
`docs/examples/fact_check.yaml` SHALL hold the `fact_check` entry (id `fact_check`, category `fact_check`, `depends_on: [topic_extraction]`, binding `extraction`, `api_key: ${GOOGLE_FACTCHECK_API_KEY}` and an `llm` block with `include: true` and a description), `docs/examples/topic_extraction.yaml` SHALL show `max_claims: 5`, and the README SHALL describe `GOOGLE_FACTCHECK_API_KEY` and that the Fact Check Tools API must be enabled on the key's Google Cloud project.

#### Scenario: Example loads
- **WHEN** the example files are loaded with their variables set
- **THEN** the registry is valid

### Requirement: Shared execution keys
`max_results`, `max_concurrency` and `cache_ttl` SHALL be accepted by both `news_search` and `fact_check`, each with its own defaults, and rejected on every other adapter; the other `news_search` keys SHALL be rejected on `fact_check`.

#### Scenario: Both analyzers in one registry
- **WHEN** a registry holds a `news_search` and a `fact_check` entry that both set `max_results`, `max_concurrency` and `cache_ttl`
- **THEN** it loads and each analyzer has its own values

#### Scenario: News-only key on fact_check
- **WHEN** a `fact_check` entry sets `num`
- **THEN** loading fails with an error naming news_search as the only adapter allowing it
