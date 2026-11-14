# job-store Specification

## Purpose
Backend-local rules for the job store: the persistence boundary, the optional SQLite history, its configuration, schema, write points, startup recovery and retention. The client-visible behaviour is the shared `persistent-job-history` contract.

## Requirements

### Requirement: Store is the only persistence boundary
The engine SHALL use jobs storage only through the `Store` interface of `internal/store`. The in-memory `Memory` store SHALL remain the default and SHALL keep its behaviour: jobs are lost on restart and `JOB_RETENTION` removes finished jobs.

#### Scenario: Default store
- **WHEN** no `JOB_STORE` is configured
- **THEN** jobs live in the `Memory` store, `Recover` ends nothing, and a swept job answers `not_found`

### Requirement: SQLite store
The SQLite store SHALL use a pure-Go driver (`modernc.org/sqlite`, no CGO), open the database in WAL mode with a busy timeout, and serialize writes through a mutex. It SHALL create the parent directory of the database file.

#### Scenario: Open a new file
- **WHEN** the store is opened on a path whose directory does not exist
- **THEN** the directory and database are created and the journal mode is `wal`

### Requirement: Store configuration
`JOB_STORE` SHALL be `memory` (default) or `sqlite`. `JOB_STORE_PATH` SHALL default to `data/jobs.db` and apply to `sqlite`. `JOB_HISTORY_RETENTION` SHALL be an optional positive Go duration, unset meaning history is kept forever, and apply to `sqlite`. An invalid value, or a database that cannot be opened, SHALL fail startup with an error naming the variable.

#### Scenario: Invalid store
- **WHEN** `JOB_STORE=postgres`
- **THEN** startup fails with an error naming `JOB_STORE`

#### Scenario: Unusable path
- **WHEN** `JOB_STORE=sqlite` and `JOB_STORE_PATH` is below a regular file
- **THEN** startup fails with an error naming `JOB_STORE_PATH`

### Requirement: Schema and migrations
The database SHALL hold a `jobs` table keyed by `id` with the columns `status`, `done`, `created_at` and `finished_at`, the full Job as JSON and the event-stream state (plan, progress, previews, activities, `last_seq`) as JSON, and a `schema_version` table. Migrations SHALL run at startup inside one transaction and be idempotent. A database with a `schema_version` newer than the server supports SHALL fail startup.

#### Scenario: Empty file and reopen
- **WHEN** an empty database file is opened, closed and opened again
- **THEN** it is migrated to the latest version once and the stored jobs are unchanged

#### Scenario: Newer database
- **WHEN** the stored `schema_version` is higher than the server's latest migration
- **THEN** opening fails with an error saying the schema is newer

### Requirement: Lossless job storage
A stored job SHALL read back so that marshalling it again gives JSON equivalent to the original, including each AnalyzerRun `result` and `finalization.result` as raw JSON with unknown members, for every analyzer type, and the event-stream state.

#### Scenario: Every analyzer type
- **WHEN** a job holding results of `techniques`, `llm_techniques`, `fact_check`, `news_search`, `topic_extraction`, `source_info`, `wikipedia`, `translation` and `propaganda_detection` and a FinalAssessment with unknown members is saved, the store is closed and reopened
- **THEN** the loaded job marshals to equivalent JSON and `last_seq` is unchanged

### Requirement: Write points
The engine SHALL save a job when it is created, with every event it emits (every AnalyzerRun, scrape or finalization change, job status change and terminal state), from a clone taken under the job's lock, with `last_seq` the seq of the last event emitted. The terminal write SHALL carry the final seq. The engine SHALL NOT save after it was closed. A failed write SHALL be logged and SHALL NOT fail the job.

#### Scenario: Terminal state
- **WHEN** a job completes after emitting events up to seq 42
- **THEN** the stored job has `done` true, its final state and `last_seq` 42

#### Scenario: Shutdown
- **WHEN** the engine is closed while an analyzer runs
- **THEN** the stored job stays `running` and is not overwritten with the aborted analyzers

### Requirement: Startup recovery
`Engine.Recover` SHALL run before the HTTP server accepts requests. It SHALL apply the shared "Jobs interrupted by a restart", "Interrupted parts" and "Interrupted part timing" rules to every persisted job with `done` false (`finished_at` and durations from the server start time, `availability` and `note` through `job.Availability`, `coverage` recomputed), write it back, clear its activities and running progress, and emit no event.

#### Scenario: Running, scraping and finalizing jobs
- **WHEN** persisted jobs were `running` with a running and a pending analyzer, `scraping`, and `finalizing`
- **THEN** each is `failed` and `done`, its running part is `failed` with `error.code` `interrupted`, its pending parts `skipped` with reason `interrupted`, settled parts are unchanged and the last seq is unchanged

### Requirement: Persisted jobs through the engine
`Engine.Get` and `Engine.Page` SHALL read persisted jobs that are not in the working set. `Engine.Subscribe` of such a job SHALL return one `job.snapshot` (job, plan, progress, previews, activities) with `seq` equal to the stored `last_seq` and then end normally. `Engine.Cancel` of it SHALL return `ErrJobFinished`.

#### Scenario: After a restart
- **WHEN** a job completed with last seq 42 and the engine is restarted on the same database
- **THEN** `Get` returns equal JSON, `Subscribe` yields one snapshot with seq 42 equal to the live snapshot, and `Cancel` returns `ErrJobFinished`

### Requirement: Retention
`JOB_RETENTION` and `Sweep` SHALL only drop jobs from the in-memory working set and the bus state, never from persisted history. When `JOB_HISTORY_RETENTION` is set, `Sweep` SHALL delete persisted done jobs whose `finished_at` is older than it, and SHALL drop a job from memory no later than that.

#### Scenario: Eviction keeps history
- **WHEN** a job finished longer ago than `JOB_RETENTION` and no history retention is set
- **THEN** it is not in memory but `Get`, `Page` and `Subscribe` still serve it

#### Scenario: History retention prunes
- **WHEN** `JOB_HISTORY_RETENTION` is 3h and a job finished 4h ago
- **THEN** after a sweep `Get` answers not found and `Page` does not list it

### Requirement: Listing reads one page
`Store.Page` SHALL order jobs newest first by `created_at` with ties by `id`, honour the status filter and the cursor, and decode no more jobs than it returns. `GET /api/v1/jobs` SHALL request at most one page plus one job.

#### Scenario: Large history
- **WHEN** 10000 jobs are stored and a page of 20 is requested
- **THEN** at most 21 jobs are decoded and the ordering index is used

### Requirement: Job visibility column
The SQLite `jobs` table SHALL have a NOT NULL boolean column `public` defaulting to 1. A startup migration SHALL add it to existing databases so that every pre-existing row is public, and running the migration again SHALL change nothing. Stored Job JSON without a `public` member SHALL decode as `public: true`.

#### Scenario: Old database migrates
- **WHEN** the server opens a database created before the column existed
- **THEN** its jobs are reported with `public: true` and are listed

#### Scenario: Migration is idempotent
- **WHEN** an already migrated database is opened again
- **THEN** it opens unchanged

### Requirement: Listing pages count public jobs only
`Page` queries SHALL apply `public = 1` in SQL before `ORDER BY created_at DESC, id` and `LIMIT`, so the page size and the cursor count public jobs only; an index SHALL cover `(public, created_at, id)` and `(public, status, created_at, id)` for the status filter. The in-memory store SHALL apply the same filter before paging, with the same ordering and cursor semantics.

#### Scenario: Interleaved jobs
- **WHEN** public jobs P1, P2, P3 and non-public jobs U1, U2 are interleaved and a client pages with `limit` 2
- **THEN** the first page holds the two newest public jobs, the next page the remaining one, and no page holds U1 or U2, in both stores

#### Scenario: Status filter
- **WHEN** a status filter is given
- **THEN** non-public jobs with that status are still left out before paging

### Requirement: Map values schema
The SQLite schema SHALL gain, as an appended migration that leaves released migrations unchanged, per-job map values: `map_country_code`, `map_country`, `map_outlet` (JSON), `map_techniques` (JSON list of name, display and, when stored, strength) and `map_eligible` (NULL until determined), indexed by `(map_country_code, created_at DESC, id)` for the public done eligible jobs (the id tie-break ascends like the job listing).

#### Scenario: Migration of an existing database
- **WHEN** a database at the previous schema version is opened
- **THEN** it is migrated in place, its jobs are unchanged and they have no map values until the backfill ran

### Requirement: Map analysis eligibility
A job SHALL be a map analysis (`map_eligible` 1) exactly when it is done and public, its `techniques` run succeeded with at least one known detected technique (weak included), and its map country is non-empty. The map country SHALL be that of the `page_outlet` of a succeeded `source_info` run when it is not null, otherwise that of the outlet-table match of `input.source.url`; a `country_code` of `""` SHALL count as none. It SHALL NOT be inferred from a TLD, language or text.

#### Scenario: Non-public job
- **WHEN** a done job with `public: false` has detected techniques and a `page_outlet` in `RU`
- **THEN** its `map_eligible` is 0

#### Scenario: Weak-only detection
- **WHEN** `techniques.result.detected` holds only `Bandwagon` with `strength: "weak"` and the map country is `DE`
- **THEN** the job is eligible with the technique set `["Bandwagon"]`

#### Scenario: Failed techniques run
- **WHEN** the `techniques` run failed and `llm_techniques` found techniques
- **THEN** the job is not eligible

#### Scenario: Outlet-table fallback
- **WHEN** `source_info` was skipped and `input.source.url` is `https://www.spiegel.de/a-1` and the outlet table lists `spiegel.de` under `DE`
- **THEN** the job is eligible with map country `DE`

#### Scenario: Page outlet without country
- **WHEN** `page_outlet.country_code` is `""`
- **THEN** the job has no map country and is not eligible, without trying the outlet table

#### Scenario: Unmatched URL
- **WHEN** `input.source.url` is `https://unknown-blog.fr/post` and no outlet matches
- **THEN** the job is not eligible

### Requirement: Map values fixed when done
The map values SHALL be computed once, when a job is saved with `done` true, and written in the same write as the done record. They SHALL never be recomputed, also when the outlet table changes or the job is saved again.

#### Scenario: Outlet table replaced later
- **WHEN** a done job was placed in `GB` by the outlet table and the table later lists its domain under `IE`
- **THEN** the job stays in `GB`

### Requirement: Map backfill
At startup (`Engine.Recover`) every stored done job without map values SHALL get them from its stored analyzer results and input, with the outlet table loaded at that time, in batches of at most 200 inside transactions. The backfill SHALL be idempotent. A failure SHALL be logged and retried at the next start and SHALL NOT prevent the server from serving or creating jobs.

#### Scenario: Job stored before the map
- **WHEN** a database holding a done job with detected techniques and a `page_outlet` in `FI` is opened and recovered
- **THEN** the map aggregate counts the job for `FI`

### Requirement: Memory store map values
The Memory store SHALL derive the same map values for the done jobs it saves, keep them until the job is evicted, and answer the same aggregate and paged list queries as the SQLite store.

#### Scenario: Same answers
- **WHEN** the same jobs are saved in a Memory and in a SQLite store
- **THEN** the aggregate and the paged lists of both are equal

### Requirement: Map queries
The store SHALL answer an aggregate query (eligible jobs grouped by `map_country_code`: count, the country name of the newest job, per-technique counts ordered by count descending then technique-name order) and a list query (newest first by `(created_at, id)`, keyset after a cursor, optional limit). A job removed by history retention or `Prune` SHALL leave both answers.

#### Scenario: Retention
- **WHEN** `Prune` deletes a done map analysis of `PL`
- **THEN** the `PL` aggregate and list no longer contain it and no map data of it remains
