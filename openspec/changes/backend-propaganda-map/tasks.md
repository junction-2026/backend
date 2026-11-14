## 1. Store
- [x] 1.1 Migration 3 (map columns, partial index); `DeriveMap`; Save writes values once; `BackfillMap`
- [x] 1.2 `MapCountries` / `MapAnalyses` for SQLite and Memory; Memory derivation and eviction
## 2. Engine and HTTP
- [x] 2.1 Engine passes the outlet table to the store, backfills in `Recover`, delegates the two queries
- [x] 2.2 `internal/httpapi/map.go` handlers and the route pair
## 3. Tests
- [x] 3.1 Derivation table, store parity, migration + backfill, retention, fixed-when-done
- [x] 3.2 HTTP tests and an engine test (counted once done, survives restart)
