# Proposal: checkable claims in topic_extraction and the fact_check analyzer

## Why

Parent feature junction-2026/project#11, child junction-2026/backend#36. `topic_extraction` returns broad topics, which are
poor search terms for existing fact-checks. The extractor now also returns short, checkable claims, and a new regular
analyzer `fact_check` looks them up in Google Fact Check Tools (ClaimReview).

## What Changes

- `topic_extraction` (llm adapter): `claims` in the JSON schema, validation, normalization (clamp, stable order, cap
  `execution.max_claims`, default 5); `config/extractor/prompt.md` defines what a claim is, with two examples.
- New adapter `fact_check` (`internal/adapter/factcheck.go`): query planning, bounded concurrency, one retry on 429/5xx,
  in-memory cache, rating table, API key redaction.
- Engine: an adapter can settle its run as skipped after `analyzer.started` (`adapter.SkipError`); used for
  `no_checkable_claims`.
- Registry: adapter `fact_check` and its execution keys; `max_claims` on the topic_extraction llm entry.
- Docs: `docs/examples/fact_check.yaml`, `max_claims` in `docs/examples/topic_extraction.yaml`, README section.

## Shared specs

Pinned `junction-2026/shared-specs@b6a2e9e8956f8957bcf59aa66433e87ef2fde71a`, change `checkable-claims-fact-check`
(capabilities `topic-extraction-result` modified, `fact-check-result` new). This change implements them and adds only
backend-local requirements (configuration, request shape, cache, engine hook, example).

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `analyzer-adapters`: claims in topic extraction, prompt, `fact_check` adapter
- `analysis-engine`: adapter-reported skip
- `analyzer-registry`: `fact_check` settings and validation, `max_claims`, examples

## Impact

`internal/adapter/{adapter,topic,factcheck}.go`, `internal/engine/run.go`, `internal/registry/registry.go`, tests,
`config/extractor/prompt.md`, `docs/examples/{fact_check,topic_extraction}.yaml`, `README.md`.
