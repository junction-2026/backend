## Why

The LLM finalizer cannot tell an analyzer that had nothing to check (e.g. no factual claims) from one that failed, so it marks harmless runs "unavailable" and drifts to `uncertain` (junction-2026/project#22, backend#74).

## What Changes

- Implements shared change `analyzer-run-availability` (shared-specs@c2c3466b90ac2115b0c17ba4162781d131fea85f): AnalyzerRun gains `availability` and `note`, computed by one mapping function when the run settles.
- LLM finalizer entries carry `availability` and `note`; `status`/`reason` and the insufficient-evidence rule are unchanged.
- `config/finalizer/prompt.md` explains availability and rewrites the `uncertain` rule.

## Capabilities

### Modified Capabilities
- `analysis-engine`: availability/note set at settle time; dependency_skipped root resolution.
- `analysis-finalizer`: LLM entries and prompt rules.

## Impact

`internal/job` (fields + `Availability`), `internal/engine` (settle and creation skips), `internal/finalizer` (`llmEntries`), `config/finalizer/prompt.md`, tests.
