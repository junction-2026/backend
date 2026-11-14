## ADDED Requirements

### Requirement: Wikipedia quote verification in normalization
After the finalizer output passed normalization and validation, the runtime SHALL set `wikipedia_quotes` to the proposed quotes verified against the job's succeeded AnalyzerRuns of the `wikipedia` adapter (shared "Wikipedia quote verification"), storing each kept quote trimmed with `title`, `url`, `revision_id` and `coverage` copied from the matching WikiLookup.

#### Scenario: Valid quote kept
- **WHEN** the finalizer proposes `{"claim": "Angela Merkel became chancellor in 2006.", "language": "en", "quote": " Chancellor of Germany from 2005 to 2021 "}` and the `en` WikiLookup of that claim is `found`, `conflicts`, with that text in its passage
- **THEN** the stored quote is "Chancellor of Germany from 2005 to 2021" with the WikiLookup's title, url, revision_id and `coverage: "conflicts"`

### Requirement: Unverifiable Wikipedia evidence in quote checks
An `aligned` or `conflicts` WikiLookup whose `evidence_sentence` is missing or not in `passage` SHALL count, and be copied into a kept quote, as `mentioned`.

#### Scenario: Unverifiable aligned entry
- **WHEN** a quote verifies against an `aligned` WikiLookup whose `evidence_sentence` is not in its `passage`
- **THEN** the kept quote has `coverage: "mentioned"`

### Requirement: Dropped Wikipedia quotes
The runtime SHALL drop every proposed quote that is not an object with string `claim`, `language` and `quote` or does not verify, logging one debug line per dropped quote with the job id and a reason but never the quote text. Dropping SHALL NOT fail finalization or trigger an LLM reply retry; an absent or non-array member gives `[]`.

#### Scenario: Paraphrase or uncovered quote dropped
- **WHEN** a proposed quote is not a substring of the passage, or comes from a `no_coverage` WikiLookup
- **THEN** it is dropped, a debug line without the quote text is logged, and finalization completes with the finalizer's verdict

#### Scenario: Malformed member
- **WHEN** the finalizer returns `"wikipedia_quotes": "see Wikipedia"`
- **THEN** finalization is `completed` with `wikipedia_quotes: []`

### Requirement: Wikipedia quotes of the orchestrator assessment
The insufficient-evidence short-circuit result SHALL carry `wikipedia_quotes: []`.

#### Scenario: Short-circuit
- **WHEN** no analyzer the finalizer would receive succeeded
- **THEN** the orchestrator-produced result has `produced_by: "orchestrator"` and `wikipedia_quotes: []`

### Requirement: Wikipedia quotes in the LLM schema and prompt
`FinalizationSchema` SHALL list `wikipedia_quotes`, an array of objects with string `claim`, `language` and `quote` and `additionalProperties: false`, in `required` (it may be empty) so the schema stays valid in strict `json_schema` mode. `config/finalizer/prompt.md` SHALL explain the four coverage labels, say that Wikipedia is never a true/false verdict, and say that Wikipedia may be quoted only verbatim through `wikipedia_quotes` and not in `user_text` otherwise.

#### Scenario: Strict schema
- **WHEN** the llm finalizer uses `response_format: json_schema`
- **THEN** the schema's `required` contains `wikipedia_quotes`

### Requirement: Wikipedia quotes tests
The finalizer tests SHALL cover a kept quote, a dropped paraphrase, a dropped `no_coverage` quote, ignored malformed members, an orchestrator-produced assessment with `[]` and the wikipedia result reaching the LLM input unchanged; the store tests SHALL cover a SQLite round trip of `wikipedia_quotes` across reopen.

#### Scenario: Test suite
- **WHEN** `go test ./...` runs
- **THEN** these tests pass

## MODIFIED Requirements

### Requirement: Response normalization
For every adapter the runtime SHALL store `verdict`, `confidence` (absent or null as null) and `user_text` unchanged, set `produced_by` to `finalizer`, set `usage` to the adapter's reported usage (`llm`: the provider's, combined after a reply retry; else null), set `attempts`, `retry_reason` ("FinalAssessment retry members") and `wikipedia_quotes` (verified quotes) itself and drop every other output member. It SHALL NOT compute or adjust the verdict or confidence.

#### Scenario: Minimal response
- **WHEN** an `http_sync` finalizer returns `{"verdict":"uncertain","user_text":"The analyzers disagree."}`
- **THEN** the stored result is `{"verdict":"uncertain","confidence":null,"user_text":"The analyzers disagree.","produced_by":"finalizer","usage":null,"attempts":null,"retry_reason":null,"wikipedia_quotes":[]}`

#### Scenario: Unknown members
- **WHEN** the output has the extra members `reasoning`, `usage` and `produced_by`
- **THEN** the stored result has no `reasoning`, `usage` is the adapter's report and `produced_by` is `finalizer`
