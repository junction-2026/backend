## Context
`fact_check` reuses the adapter plumbing of `propaganda_detection` and the `llm` client (`noRedirects`, `truncate`,
`ContextError`). The engine had no adapter-reported skip (`internal/engine/run.go` skipped only by dependency).

## Decisions
- **Skip hook**: an adapter returns `*adapter.SkipError{Reason}`; `run.exec` settles the run as `skipped` with that reason
  (one `analyzer.skipped` event, `started_at` kept) and advances the DAG. The hook is deliberately minimal so project#10
  (`no_searchable_topics`) can reuse it; it was not on main when this was written.
- **Cache**: process-wide map inside `Executor` (lazy), key `(languageCode, query)`, value = decoded provider claims, expiry
  from the first writer's `cache_ttl`. Failures are never stored. Expired entries are swept on write once the map is large.
- **Failure policy** (shared design): 400/401/403 abort the analyzer (`adapter_error`, no further requests: queries not yet
  sent check a cancelled context first); 429/5xx retried once; other failures fail only the query.
- **Key hygiene**: the key goes only in `key=`; transport errors are never formatted (net/http errors embed the URL);
  provider messages are redacted (raw and URL-encoded key) before truncation.
- **`max_claims`** is one yaml key with two defaults (5 for the extractor, 3 for fact_check), applied by accessor.
- **Rating table** is a package-level map, lowercase terms; unknown terms are `unrated`.
