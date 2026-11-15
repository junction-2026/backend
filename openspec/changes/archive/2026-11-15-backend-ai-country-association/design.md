## Context
Shared `country-association` decides that the step is a job-level orchestrator step (like `risk`), not an AnalyzerRun,
running concurrently with finalization and settled before `done`. The map used one `map_country_code` column per job
(`internal/store/sqlite.go` migration 3).

## Decisions
1. **Step = engine hook + adapter.** `engine.CountryAssociator` (interface, nil: not configured) is implemented by
   `adapter.CountryAssociator`, which reuses the existing OpenAI-compatible client (`llmChat`: one transport retry on 429/5xx,
   one reply retry on invalid JSON/schema when time is left, provider messages redacted of the API key) with its own reply
   schema `{"countries":[{"country_code"}]}` (json_schema response format). Its own context timeout gives `timeout`.
2. **Barrier.** `barrierLocked` sets the skip (`no_detected_propaganda` first, then `not_configured`) synchronously or
   starts the step on a job clone; `completeLocked` finishes the job once finalization is not running and the association
   is set. Settling the association persists the job without an event. `abortLocked` fails a running step with `cancelled`;
   `job.Record.Interrupt` fails it with `interrupted` when the job passed the barrier (`risk` set) without an association.
3. **ISO table** `internal/country/iso3166-1.csv` (249 officially assigned codes, English short names) is embedded and
   parsed by `country.Load` at start; a parse error stops the server. Model names are never used.
4. **Storage.** Migration 4: `job_map_countries(job_id, position, country_code, country, created_at)` with index
   `(country_code, created_at DESC, job_id)`; `jobs.map_countries` (JSON list with `basis`) for reading items;
   `jobs.country_attempts`. Existing eligible rows get their country as position 0 with basis `outlet`. The old partial
   index is dropped; `map_country_code`/`map_country` are no longer read. `map_outlet`/`map_techniques` are now also written
   for a done public job with detected propaganda but no country (eligible 0) so the backfill reuses the fixed outlet;
   SQL NULL there means not recorded (rows from before), and then the current outlet table is used (documented shortcut).
5. **Backfill.** Started by `Engine.Recover` when the step is configured: batches of `COUNTRY_BACKFILL_BATCH` (default 20),
   one job at a time, a pause between batches (10 s), at most 3 attempts per job (`country_attempts`), fewest attempts first
   then newest; it ends when no candidate is left or the engine closes (`Engine.Close` waits for it). Candidates are selected
   in SQL (done, public, succeeded `techniques` with a non-empty `detected`, association absent/null/failed). The update is
   conditional on the association still being absent or failed, in one write transaction that also replaces the job's map
   countries, and updates the live record of the working set under its lock, so a job finished live is never overwritten.
6. **Distinct total.** `Store.MapCountries` returns the aggregates and the number of distinct map analyses.

## Risks / Trade-offs
- The candidate query scans the done public rows' JSON per batch (shortcut comment; add a flag column if history grows).
- A job waits for the step when it is slower than finalization (bounded by `COUNTRY_ASSOCIATION_TIMEOUT`).
