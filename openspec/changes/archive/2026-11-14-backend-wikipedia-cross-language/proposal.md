# Proposal: Wikipedia cross-language agreement (backend)

## Why

Parent feature junction-2026/project#47, child issue junction-2026/backend#125. Implements the shared change
`wikipedia-cross-language` (shared-specs @ 5981c9fdec37dc8664689f5ff2595ae797da618a, capability
`wikipedia-lookup-result`): every ClaimLookup carries `cross_language` (`agree`, `discrepancy`, `gap` or null) and
`cross_language_differences`. The contract is shared; the recognition tables, code layout and tests are backend-local.

## What Changes

- `internal/adapter/wikipedia_crosslang.go` (new): deterministic date/number extractor on verbatim passages and the
  pure comparison of a ClaimLookup's WikiLookups.
- `internal/adapter/wikipedia.go`: `claimLookup` gains the two members, set once after all lookups finished.

Not changed: shared specs, frontend, the wikipedia cache format, `coverage`/`evidence_sentence`, `wikipedia_text.go`.

## Impact

backend only; additive JSON members. No LLM, no translation, no extra network request.
