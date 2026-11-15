## 1. Store
- [x] 1.1 `job.Job.SourceLanguage` / `job.ReadSourceLanguage`; adapter delegates
- [x] 1.2 Migration 5 (`language` column, index) with in-transaction batched backfill
- [x] 1.3 Save writes `language` (SQLite column, Memory map); `Page` language filter in both stores
## 2. Engine and API
- [x] 2.1 `Engine.Page` language argument; recovery and state changes keep the stored value consistent via `Save`
- [x] 2.2 `GET /api/v1/jobs` `language` parsing and validation; JobSummary `language`
## 3. Tests
- [x] 3.1 Store tests: filter with status, q and paging in both stores; SQL-only filtering (decoded count, index in plan); value follows the translation run and interruption
- [x] 3.2 Migration test: populated version 4 database (de, en, null, no translation, failed translation, non-public, undecodable, more than two batches), rollback on failure, reopen idempotent, same listing after upgrade
- [x] 3.3 Handler tests: valid, blank, case and white space, malformed values, `zz`, combinations with q, status and paging
