## Purpose
Backend-local rules for the job store: the persistence boundary, the optional SQLite history, its configuration, schema, write points, startup recovery and retention. The client-visible behaviour is the shared `persistent-job-history` contract.

## ADDED Requirements

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
