# Proposal: Wikipedia coverage label and verified Wikipedia quotes (backend)

## Why

Parent feature junction-2026/project#46, child issue junction-2026/backend#120. Implements the shared change
`wikipedia-passage-coverage` (shared-specs @ 9efc1c8cf3b4f941c4640b864b8d5287eefb99dc, capabilities
`wikipedia-lookup-result`, `analysis-jobs-api`, `analyzer-service-protocol`): every WikiLookup carries a
deterministic `coverage` label and a verbatim `evidence_sentence`, and the FinalAssessment carries
`wikipedia_quotes` that the orchestrator verified against the passages. The contract is shared; placement of the
algorithm, Go types, the LLM schema, prompt and analyzer description, logging and tests are backend-local.

## What Changes

- `internal/adapter/wikipedia_coverage.go` (new): pure labelling function, sentence splitting, standalone numbers,
  downgrade re-check; `wikipedia.go` gains the members `coverage`/`evidence_sentence` and one call in `personalize`.
- `internal/job`: `FinalAssessment.WikipediaQuotes` and `WikipediaQuote`.
- `internal/finalizer`: verifies proposed `wikipedia_quotes` and drops the rest with a debug log line; orchestrator
  assessments carry `[]`.
- `internal/adapter/llm.go`: `FinalizationSchema` lists `wikipedia_quotes` (required, may be empty).
- `config/finalizer/prompt.md` and `docs/examples/wikipedia.yaml` `llm.description`.

Not changed: shared specs, frontend, the wikipedia cache format (labels are recomputed per claim), cross-language
ClaimLookup members (#47).

## Impact

backend only; additive JSON members. Results stored before this change lack the members, which the contract
defines as `no_coverage` and `[]`.
