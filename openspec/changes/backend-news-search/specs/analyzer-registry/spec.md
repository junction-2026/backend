## ADDED Requirements

### Requirement: news_search settings
The registry SHALL accept `execution.adapter: news_search` with the keys `base_url` and `api_key` (required, `${VAR}`-expanded), `languages` (default `[source, en]`), `translate.api_key`, `translate.model` and `translate.endpoint`, and the limits `max_topics`, `max_news`, `num`, `min_results`, `per_domain`, `max_results`, `max_concurrency`, `cache_ttl` and `timeout`. `translate.api_key` SHALL be required when a language other than `source` is configured.

#### Scenario: Defaults
- **WHEN** only `base_url` and `api_key` and `translate.api_key` are set
- **THEN** the analyzer loads with languages `[source, en]`, max_topics 3, max_news 2, num 8, min_results 2, per_domain 2, max_results 12, max_concurrency 4, cache_ttl 6h and timeout 20s

### Requirement: news_search settings are validated
A non-positive integer or duration, an unknown language token, a missing required key or a `news_search` key on another adapter SHALL fail startup with a message naming the key.

#### Scenario: Invalid values
- **WHEN** `num: 0`, `languages: [german]`, a missing `api_key` or a missing `translate.api_key` with `languages: [source, en]` is configured
- **THEN** startup fails and the message names `execution.num`, `execution.languages`, `execution.api_key` or `execution.translate.api_key`

#### Scenario: Source only needs no translation key
- **WHEN** `languages: [source]` and no `translate` block
- **THEN** the analyzer loads

### Requirement: news_search dependencies and bindings
A `news_search` analyzer SHALL list `topic_extraction` in `depends_on` and bind `input.bindings.topics: $analyzers.topic_extraction`. It MAY bind `translation: $analyzers.<id>`, which SHALL be in `depends_on`. Other bindings, `endpoint`, `method`, `handler` and `poll_interval` SHALL be rejected.

#### Scenario: Missing topics binding
- **WHEN** `news_search` has no `topics` binding or does not depend on `topic_extraction`
- **THEN** startup fails naming `input.bindings.topics` or `depends_on`

#### Scenario: Translation binding outside depends_on
- **WHEN** `translation: $analyzers.translation` is bound but `translation` is not in `depends_on`
- **THEN** startup fails naming the binding
