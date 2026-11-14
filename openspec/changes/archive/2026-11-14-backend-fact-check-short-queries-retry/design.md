# Design

- **Where the retry runs**: inside the goroutine of the original search, after its answer and while it still holds the
  `max_concurrency` slot, so concurrency stays bounded and the retry runs only after the original's answer. No LLM call.
- **Retry query** is computed at planning time (`plannedQuery.short`): `firstEntities(entities, 2)` for the claim-language
  search, first 3 `strings.Fields` words of `query_en` for the English search; dropped when empty or equal to `q` after
  collapsing whitespace, case-insensitively.
- **Trigger**: the original `Status == "succeeded"` and `Claims == 0` (cache hit or provider answer). Failed searches and
  retries never get a retry. A retry goes through the same `search` (cache, single 429/5xx backoff, fatal 400/401/403).
- **Ordering**: after all searches finish, retries are interleaved directly after their original; `assembleFactChecks`
  then sees the retry in planning order with `query` = retry `q` and the original's `for_claim`.
- **Accounting**: `requests_used` is the existing atomic request counter (retries and backoff attempts included); `cached`
  counts all Queries with `cached: true`. Default bound: 6 planned + 6 retries = 12.
