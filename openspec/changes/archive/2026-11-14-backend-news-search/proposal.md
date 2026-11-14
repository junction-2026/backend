# Proposal: `news_search` analyzer (backend)

## Why

Parent feature junction-2026/project#10, child issue junction-2026/backend#32. Implements the shared change
`news-search-result` (shared-specs @ 606cc9356f539952b9c63f2004353aa3c388a452): an analyzer that searches news and
web coverage (Serper) of the topics and news items found by `topic_extraction`, in the text's language and in
English. The result shape and the `no_searchable_topics` skip reason are the shared contract; everything below
is backend-local (registry keys, provider requests, cache, retries, concurrency, query translation, engine hook).

## What Changes

- Registry: adapter `news_search` with its execution keys, defaults, bindings and startup validation.
- Engine: an adapter can settle a running run as `skipped` with a reason (narrow hook in `internal/engine/run.go`).
- Adapter `news_search`: query building, Mistral query translation, Serper news search with web fallback,
  bounded concurrency, one retry of 429/5xx, 401/403 abort, in-memory TTL cache, merging, credential redaction.
- `docs/examples/news_search.yaml`, README section "News coverage analyzer", stub-server tests.

Not changed: `internal/job`, `internal/httpapi`, the translation analyzer, shared specs.

## Impact

backend only. One new analyzer; every job that selects it spends Serper quota (see README cost formula).
