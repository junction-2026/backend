## 1. Step
- [x] 1.1 `internal/country` ISO 3166-1 table (data file, loader, startup error)
- [x] 1.2 `adapter.CountryAssociator`: prompt, reply parsing, validation, errors without credentials
- [x] 1.3 Engine barrier wiring, completion after both settled, cancellation, restart interruption
- [x] 1.4 `COUNTRY_ASSOCIATION_*` and `COUNTRY_BACKFILL_BATCH` configuration
## 2. Model and store
- [x] 2.1 `Job.country_association` (null when missing), Clone, Interrupt
- [x] 2.2 Migration 4, multi-country `DeriveMap`, Save, BackfillMap, Prune, queries for SQLite and Memory
- [x] 2.3 `CountryBackfill` / `SetCountryAssociation` (attempts, conditional update, map re-derivation)
## 3. API
- [x] 3.1 MapAnalysis `countries`, list country, nullable outlet, empty `source.url`; distinct `analyses`
## 4. Tests
- [x] 4.1 Adapter tests with a fake LLM HTTP server (single, several, no country, invalid codes, max 10, invalid output, HTTP errors, timeout, cancel, prompt)
- [x] 4.2 Engine tests (concurrent with finalization, skipped, failure isolation, cancel, interruption, backfill success/retry/skips)
- [x] 4.3 Store and HTTP tests (multi-country aggregates and lists, pasted text item, migration, conditional backfill update)
