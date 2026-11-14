## MODIFIED Requirements

### Requirement: LLM analyzer entries
There SHALL be one entry per AnalyzerRun of an analyzer with `llm.include: true`, in registry id order, as compact JSON `{id, description, status, availability, note?, result?}` when succeeded and `{id, description, status, reason, availability, note}` when failed or skipped, where `reason` is `error.code` or the skip reason and `note` is omitted when null. `availability` and `note` SHALL come from the same mapping function as the API. Analyzers without `llm.include: true` SHALL NOT appear.

#### Scenario: Opted-in analyzers only
- **WHEN** `pd` succeeded, `claims` failed with `adapter_error`, `other` was not selected (all three opted in) and `text_stats` has no `llm` block
- **THEN** the user message has exactly the entries for `claims` (`reason: adapter_error`, `availability: unavailable`, no `result`), `other` (`reason: not_selected`, `availability: not_applicable`) and `pd` (with its `result`, `availability: ran`, no `note`), each with its configured description, and no `text_stats`

#### Scenario: Nothing to check
- **WHEN** `topic_extraction` succeeded with no topics or claims, `fact_check` is skipped with `no_checkable_claims`, `news_search` with `no_searchable_topics` and `source_info` with `dependency_skipped`
- **THEN** the three skipped entries have `availability: not_applicable` and `source_info.note` equals `news_search.note`

## ADDED Requirements

### Requirement: Evidence rule unchanged by availability
`anySucceeded` and the insufficient-evidence short-circuit SHALL remain unchanged: only succeeded entries (`availability: ran`) count as evidence.

#### Scenario: Only failures
- **WHEN** every opted-in run failed or was skipped with `dependency_failed`
- **THEN** no entry counts as evidence and the short-circuit applies as before

### Requirement: Availability prompt rules
`config/finalizer/prompt.md` SHALL replace "Some may be marked unavailable; do not guess their results." with rules: each analyzer has `availability`; `ran` = produced a result; `not_applicable` = nothing for it to check in this text (see `note`), never describe it as unavailable, failed or missing, it must not lower confidence or by itself lead to `uncertain`; `unavailable` = it failed, do not guess its result.

#### Scenario: System message
- **WHEN** the LLM finalizer calls a stub LLM
- **THEN** the system message contains the availability rules and not the old sentence

### Requirement: Mentioning skipped analyzers
The prompt SHALL say to mention a `not_applicable` analyzer only if it helps the reader (e.g. "The post makes no specific factual claims that could be checked.") and an `unavailable` analyzer only if it would have mattered for this text.

#### Scenario: Mention rules present
- **WHEN** the prompt file is read
- **THEN** it contains both mention rules

### Requirement: Uncertain rule
The `uncertain` rule in `config/finalizer/prompt.md` SHALL read "evidence is weak, mixed or contradictory, or an analyzer that applies to this text is unavailable."

#### Scenario: New uncertain rule
- **WHEN** the LLM finalizer calls a stub LLM
- **THEN** the system message contains the new `uncertain` rule

### Requirement: Availability tests
Tests SHALL cover a table-driven mapping unit test, a finalizer test with a stub LLM asserting entries and the system message, an HTTP API test asserting `availability`/`note` on `GET /api/v1/jobs/{id}`, and an event test asserting identical values in `analyzer.*` `data.analyzer`, all with stubs only.

#### Scenario: Offline suite
- **WHEN** `go test ./...` runs without network access
- **THEN** the availability tests pass
