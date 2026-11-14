# Proposal: fact_check short claim queries, plan/decision claims, shorter retry

## Why

Parent feature junction-2026/project#13, child junction-2026/backend#44. Google Fact Check Tools `claims:search` matches
keywords of recorded claim texts, so long extractor queries ("EU beschließt Bargeld abschaffen 2027") return 0 where
"EU Bargeld abschaffen" finds reviews. The extractor also drops plans and decisions ("Die EU will das Bargeld abschaffen")
as predictions.

## What Changes

- `config/extractor/prompt.md`: 2-4 word `query`/`query_en` with leave-out rules, plans/decisions are claims, forecasts are
  not, three new examples. The claims JSON schema, validation and normalization are unchanged.
- `fact_check` adapter: a search that succeeds with 0 claims (fresh or cached) gets exactly one shorter retry built in code
  (first 2 non-blank entities for the claim-language search, first 3 words of `query_en` for the English search); Query
  gets `retry_of`; retries are ordered after their original, counted, cached and mapped into `fact_checks`.

## Shared specs

Pinned `junction-2026/shared-specs@af7633cd908507f52322a73f7adeedddf96c45f8`, change `fact-check-short-queries-retry`
(`topic-extraction-result`, `fact-check-result`). This change implements them; it adds only backend-local requirements.

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `analyzer-adapters`: extractor prompt, fact check shorter retry, result mapping and list assembly

## Impact

`config/extractor/prompt.md`, `internal/adapter/factcheck.go`, `internal/adapter/factcheck_test.go`,
`internal/adapter/topic_prompt_test.go`. No registry, engine or config change.
