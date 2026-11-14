## ADDED Requirements

### Requirement: term_context registry entry
The registry SHALL accept an analyzer with `execution.adapter` `term_context` only with trigger `on_dependencies_succeeded`, `depends_on` including `topic_extraction`, `translation` and `news_search`, and `input.bindings` of exactly `extraction`, `translation` and `news`, each `$analyzers.<id>` of a `depends_on` entry.

#### Scenario: Missing dependency
- **WHEN** the entry's `depends_on` lacks `news_search`
- **THEN** loading fails naming the analyzer and `news_search`

### Requirement: term_context registry keys
The `term_context` entry SHALL accept the LLM keys of `llm_techniques` except `prompt_file` (the prompts are part of the backend), the Wikipedia transport keys of the `wikipedia` adapter (`user_agent` required, `base_url_template`, `cache_dir`, `cache_ttl`, `max_concurrency`) and `max_input_chars`, `max_terms`, `in_post_max_chars`, `quote_max_chars` and `elsewhere_max`. It SHALL reject these keys on other adapters, and any other adapter's keys on `term_context`.

#### Scenario: Valid entry
- **WHEN** an enabled entry with the dependencies, bindings, LLM keys and `user_agent` is loaded
- **THEN** loading succeeds without any prompt file being read

#### Scenario: Foreign key
- **WHEN** a `term_context` entry sets `prompt_file` or `min_check_worthiness`
- **THEN** loading fails naming the allowed keys

### Requirement: term_context defaults
`max_terms` SHALL default to 5, `in_post_max_chars` to 300, `quote_max_chars` to 400 and `elsewhere_max` to 3; each SHALL be a positive integer. `timeout` SHALL default to 60s, `cache_ttl` to 168h and `max_concurrency` to 2. A deployment that does not want term context SHALL simply have no `term_context` entry (or `enabled: false`); no other setting is needed.

#### Scenario: Defaults
- **WHEN** an entry sets none of these keys
- **THEN** the values are 5, 300, 400, 3, 60s, 168h and 2

#### Scenario: Invalid limit
- **WHEN** `max_terms` is 0
- **THEN** loading fails with `execution.max_terms must be a positive integer`
