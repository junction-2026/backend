# Tasks

## 1. Store
- [x] 1.1 `Store` interface with history methods; `Memory` implements them as no-ops (default unchanged)
- [x] 1.2 `SQLite` store: modernc driver, WAL, busy timeout, write mutex, schema + migrations, Save/Load/Page/Unfinished/Prune
- [x] 1.3 Store tests: lossless round trip across reopen, migrations, page decode bound and index use, prune, concurrency

## 2. Job and events
- [x] 2.1 `interrupted` reason and code, `Record.LastSeq`, `Record.Interrupt`
- [x] 2.2 `Bus.Closed`

## 3. Engine and HTTP
- [x] 3.1 Save at creation and at every event; no writes after Close
- [x] 3.2 `Recover`; `Get`, `Page`, `Subscribe`, `Cancel` resolve persisted jobs; `Sweep` retention rules
- [x] 3.3 `GET /jobs` pages through `Store.Page`
- [x] 3.4 Engine tests: restart of done, running, finalizing and scraping jobs, eviction vs history retention, concurrency

## 4. Config and docs
- [x] 4.1 `JOB_STORE`, `JOB_STORE_PATH`, `JOB_HISTORY_RETENTION`; main wiring and recovery before listening
- [x] 4.2 README configuration table, Docker volume, interrupted-job behaviour; Dockerfile note
- [x] 4.3 `go test -race ./...`, `scripts/validate-openspec.sh`
