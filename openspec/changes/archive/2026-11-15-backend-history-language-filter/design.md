## Context
Shared `analysis-jobs-api` "Job list language filter", "Job list language paging", "Job list language scope" and
"JobSummary language" (shared-specs @ 4d2ab18). Local `job-store` "Listing reads one page" requires a page to be read
without decoding the whole history.

## Decisions
1. **One rule, in package `job`.** `adapter` imports `store`, so `store` cannot call `adapter.JobSourceLanguage`. The rule
   (succeeded `translation` run, `source_language` trimmed and lower-cased, `^[a-z]{2}$`, else unknown) moves to
   `job.Job.SourceLanguage` with `job.ReadSourceLanguage`; the adapter functions delegate, so behaviour is unchanged.
2. **Computed at every write.** `Save` (both stores) derives the language from the job it writes. Every state change of a
   job and the startup recovery (`Interrupt`, then `Save`) already pass through `Save`, so no separate engine hook is
   needed: an interrupted translation run is `failed` and yields null, a succeeded run never changes and keeps its code.
3. **SQLite column and index.** Migration 5 adds `language TEXT` (NULL: unknown) and
   `jobs_public_language_created (public, language, created_at DESC, id)`; `Page` adds `language = ?` or
   `language IS NULL` to the WHERE clause before ORDER BY / LIMIT.
4. **Backfill inside the migration.** `backfills[4]` runs after `migrations[4]` in the migration transaction: rows are read
   500 at a time ordered by `id`, each `data` is decoded and the language set when known. An undecodable job keeps NULL.
   A failure rolls back the column, index, backfill and version together; the next start retries. A migrated database
   skips it (version 5).
5. **Memory store.** `Save` records the language per id in a `sync.Map` (no store mutex under the caller's job lock);
   `Page` filters on it; `EvictFinished` drops it.
6. **HTTP.** `language` is trimmed and lower-cased; empty means no filter; `unknown` or `^[a-z]{2}$` is passed to `Page`;
   anything else is 400 `invalid_request`. `summarize` sets `language` from the listed job (same rule as the stored value),
   null when unknown; the member is always present.

## Risks
- Case-insensitive parsing uses Unicode lower-casing, so a value such as the Kelvin sign followed by `e` reads as `ke`;
  harmless (a well-formed code).
