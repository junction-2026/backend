# Design

- **Adapter-reported skip.** `adapter.Skip{Reason}` is an error type; `engine.exec` settles a running run as
  `skipped` (reason, `result: null`) when `Execute` returns it, so the stream is `analyzer.started` then
  `analyzer.skipped`. Dependents follow the existing `dependency_skipped` rule. The reason constant lives in the
  adapter package (`internal/job` is not touched).
- **Cache and in-flight sharing** share one process-wide map on the `Executor` keyed by (endpoint, q, hl, gl).
  An entry is either in flight (waiters block on it) or a successful response with an expiry. Failures are
  removed, never cached. A waiter whose leader died of context cancellation retries on its own context.
  Waiters count as `cached` (they sent no request). The map is capped at 1024 entries: inserting beyond that evicts one completed entry (in-flight ones are never evicted).
- **Fallback failure is best-effort.** If the web fallback of an entry fails after the news search succeeded with
  fewer than `min_results`, the entry stays `succeeded` with `endpoint: news` and its news results; both attempts
  are still counted in `searches_used`. A rejected key (401/403) fails the run either way.
- **Translation is one request per target language.** The user message is a JSON array of the originals; the
  reply must be a JSON array of the same length of non-empty strings, else the batch is `translation_failed`.
  Translation requests share the per-run concurrency limit and the 429/5xx retry, not `searches_used`.
- **Ordering.** Articles sort by (provider rank, has parseable date first, newest first, SearchQuery order) —
  a total order; unparseable dates sort after parseable ones at equal rank.
- **Fatal errors** (401/403) cancel a derived context for the remaining searches; the first one wins.
- Translation results are not cached (only Serper responses, as specified).
