## Context
`fact_check` reuses the adapter plumbing of `propaganda_detection` and the `llm` client (`noRedirects`, `truncate`,
`ContextError`). The engine had no adapter-reported skip ( skipped only by dependency).

## Decisions
- **Skip**: `factCheck` returns `&adapter.Skip{Reason: "no_checkable_claims"}`, the hook project#10 added; the engine is unchanged.
- **Cache**: process-wide map inside `Executor`, key `(languageCode, query)`, value = decoded provider claims, expiry from
  the writer's `cache_ttl`. Failures and answers over 256 KiB are never stored. Hard cap `maxCacheEntries` (1024, as
  news_search): at the cap an insert drops expired entries, then the soonest-expiring one.
- **Shared keys with news_search**: `max_results`, `max_concurrency` and `cache_ttl` are one `Execution` field each (main's
  types); each adapter applies its own defaults; the "only allowed for" checks accept them for both adapters.
- **Failure policy** (shared design): 400/401/403 abort the analyzer (`adapter_error`, no further requests: queries not yet
  sent check a cancelled context first); 429/5xx retried once; other failures fail only the query.
- **Key hygiene**: the key goes only in `key=`; transport errors are never formatted (net/http errors embed the URL);
  provider messages are redacted (raw and URL-encoded key) before truncation.
- **`max_claims`** is one yaml key with two defaults (5 for the extractor, 3 for fact_check), applied by accessor.
- **Rating table** is a package-level map, lowercase terms; unknown terms are `unrated`.
