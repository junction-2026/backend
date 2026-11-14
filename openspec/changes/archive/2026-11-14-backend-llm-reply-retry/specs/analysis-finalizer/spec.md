## MODIFIED Requirements

### Requirement: Response normalization
For every adapter the runtime SHALL store `verdict`, `confidence` (absent or null as null) and `user_text` unchanged, set `produced_by` to `finalizer`, set `usage` to the usage the adapter reported (`llm`: the provider's `usage`, combined after a reply retry; otherwise null), set `attempts` and `retry_reason` itself ("FinalAssessment retry members") and drop every other output member. It SHALL NOT compute or adjust the verdict or confidence.

#### Scenario: Minimal response
- **WHEN** an `http_sync` finalizer returns `{"verdict":"uncertain","user_text":"The analyzers disagree."}`
- **THEN** the stored result is `{"verdict":"uncertain","confidence":null,"user_text":"The analyzers disagree.","produced_by":"finalizer","usage":null,"attempts":null,"retry_reason":null}`

#### Scenario: Unknown members
- **WHEN** the output has the extra members `reasoning`, `usage` and `produced_by`
- **THEN** the stored result has no `reasoning`, `usage` is the adapter's report and `produced_by` is `finalizer`

## ADDED Requirements

### Requirement: FinalAssessment retry members
The runtime SHALL set `attempts` to the number of LLM requests whose replies were judged (1 or 2) and `retry_reason` to the reply retry reason (null without a retry) for an `llm` finalizer, and both to null for orchestrator-produced results and the `http_sync` and `http_async` finalizers. Output members named `attempts` or `retry_reason` SHALL be dropped.

#### Scenario: Output members named attempts and retry_reason
- **WHEN** an `http_sync` finalizer returns `{"verdict":"detected","user_text":"...","attempts":5,"retry_reason":"x"}`
- **THEN** the stored result has `attempts: null` and `retry_reason: null`

#### Scenario: Insufficient evidence
- **WHEN** no analyzer the finalizer would receive succeeded
- **THEN** the orchestrator-produced result has `attempts: null` and `retry_reason: null`

#### Scenario: LLM first reply valid
- **WHEN** the `llm` finalizer's first reply is valid
- **THEN** the stored result has `attempts: 1` and `retry_reason: null`
