# Tasks: backend-history-search

## 1. Store
- [x] 1.1 `store.Matches` and `Page(status, q, after, limit)` in the Memory store
- [x] 1.2 SQLite filtered scan without migration
- [x] 1.3 Extend `TestPageMatchesMemoryOrderFilterAndCursor` with searches; scan past limit and restart test

## 2. Engine and HTTP
- [x] 2.1 `Engine.Page` passes `q`
- [x] 2.2 `listJobs` trims and validates `q` (200 code points, 400 `invalid_request`)
- [x] 2.3 `summarize` sets `excerpt`
- [x] 2.4 Tests for every shared scenario against a persisted SQLite history served after a restart
