# Proposal: `wikipedia` analyzer (backend)

## Why

Parent feature junction-2026/project#29, child issue junction-2026/backend#91. Implements the shared change
`wikipedia-claim-lookup` (shared-specs @ 686777ee4bc9db7b279b51d3c00a219ff02d39a1, capability
`wikipedia-lookup-result`): for each checkable claim of `topic_extraction`, the Wikipedia page and revision read in the
post's language and in English, with a verbatim passage. The result shape, skip reason, failure semantics and
rendering rules are the shared contract; everything below is backend-local (registry keys, MediaWiki requests,
User-Agent, on-disk cache, concurrency, tests).

## What Changes

- Registry: adapter `wikipedia` with its execution keys, defaults, bindings, `${VAR}` expansion and startup validation.
- Adapter `wikipedia`: claim qualification, wiki selection, search mode, MediaWiki search plus page request,
  page selection and disambiguation, passage and matched entities/numbers, on-disk cache, in-run reuse, bounded
  concurrency, one retry of 429/5xx, all-or-nothing failure.
- `docs/examples/wikipedia.yaml`, README section "Wikipedia analyzer", Dockerfile note on `DATA_DIR`, offline
  httptest tests with MediaWiki-shaped fixtures.

Not changed: shared specs, `internal/job`, `internal/engine`, `internal/httpapi`, other analyzers.

## Impact

backend only. One new optional analyzer; it sends at most 2 searches and 2 page requests per qualifying claim
(max_claims, default 3), none for cached lookups. The cache needs a directory writable by uid 65532.
