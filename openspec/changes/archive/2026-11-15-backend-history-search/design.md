# Design: backend-history-search

## Decisions
1. **Filtered scan, not a SQL function.** With a non-empty search, `SQLite.Page` runs the usual ordered query (public,
   status, cursor in SQL) without `LIMIT`, decodes each row and keeps the ones `store.Matches` accepts until it has
   `limit`. It needs no driver-specific function registration and shares the exact matching code with the Memory store.
2. **No migration.** Running in parallel with #64 (`backend-manipulation-risk-score`), the append-only migration list is
   untouched; matching reads the stored Job JSON.
3. **Unicode lowercase in Go** (`strings.ToLower` on both sides, `strings.Contains`): SQLite `lower()`/`LIKE` are
   ASCII-only and treat `%`/`_` as wildcards, so they are not used.
4. **Ordering and cursor unchanged**: the filter only drops rows from the ordered scan.
5. **Excerpt computed on read** in `summarize` from the stored job, so jobs stored before this change get one too.

## Risks
- A search decodes up to every public row of the history (bounded by History retention). Acceptable at current sizes;
  a denormalized lowercase column could be added later if needed.
