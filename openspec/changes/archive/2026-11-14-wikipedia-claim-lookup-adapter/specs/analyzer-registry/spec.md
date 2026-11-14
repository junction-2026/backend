## ADDED Requirements

### Requirement: wikipedia settings
The registry SHALL accept `execution.adapter: wikipedia` with the keys `min_check_worthiness` (default 0.4, between 0 and 1), `max_claims` (default 3), `max_hits` (search hits considered, default 5, at most 20), `max_passage_chars` (default 1500), `max_concurrency` (default 2), `cache_dir`, `cache_ttl` (default 168h), `user_agent`, `base_url_template` and `timeout` (default 20s). `${VAR}` SHALL be expanded in `user_agent`, `base_url_template` and `cache_dir` of an enabled analyzer.

#### Scenario: Defaults
- **WHEN** only `user_agent` is set
- **THEN** the analyzer loads with min_check_worthiness 0.4, max_claims 3, max_hits 5, max_passage_chars 1500, max_concurrency 2, cache_ttl 168h, timeout 20s and base_url_template `https://{lang}.wikipedia.org/w/api.php`

### Requirement: wikipedia settings are validated
A value out of range, a non-positive duration, a missing or empty `user_agent` (also after `${VAR}` expansion), a `base_url_template` without `{lang}`, an execution key of another adapter, or a `wikipedia` key on another adapter SHALL fail registry load with a message that names the analyzer and the key.

#### Scenario: Invalid values
- **WHEN** `max_hits: 0`, `min_check_worthiness: 1.5`, `cache_ttl: 0s`, a missing `user_agent` or `base_url_template: http://x/api.php` is configured
- **THEN** load fails naming analyzer `wikipedia` and `execution.max_hits`, `execution.min_check_worthiness`, `execution.cache_ttl`, `execution.user_agent` or `{lang}`

### Requirement: wikipedia cache directory
`cache_dir` SHALL default to `$DATA_DIR/wikipedia` when `DATA_DIR` is set and non-blank, and to `./var/wikipedia` otherwise.

#### Scenario: DATA_DIR
- **WHEN** `DATA_DIR=/data` and `cache_dir` is absent
- **THEN** the cache directory is `/data/wikipedia`

### Requirement: wikipedia dependencies and bindings
A `wikipedia` analyzer SHALL have `trigger: on_dependencies_succeeded`, list `topic_extraction` in `depends_on` and bind `input.bindings.extraction: $analyzers.<id>`. It MAY bind `translation: $analyzers.<id>`, which SHALL be in `depends_on`. Other bindings SHALL be rejected.

#### Scenario: Missing extraction binding
- **WHEN** `wikipedia` has no `extraction` binding or does not depend on `topic_extraction`
- **THEN** load fails naming `input.bindings.extraction` or `depends_on`
