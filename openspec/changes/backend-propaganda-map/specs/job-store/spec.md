## ADDED Requirements

### Requirement: Map values schema
The SQLite schema SHALL gain, as an appended migration that leaves released migrations unchanged, per-job map values: `map_country_code`, `map_country`, `map_outlet` (JSON), `map_techniques` (JSON list of name, display and, when stored, strength) and `map_eligible` (NULL until determined), indexed by `(map_country_code, created_at DESC, id)` for the public done eligible jobs (the id tie-break ascends like the job listing).

#### Scenario: Migration of an existing database
- **WHEN** a database at the previous schema version is opened
- **THEN** it is migrated in place, its jobs are unchanged and they have no map values until the backfill ran

### Requirement: Map analysis eligibility
A job SHALL be a map analysis (`map_eligible` 1) when it is done and public, its `techniques` run succeeded with at least one known detected technique (weak included) and whose map country, from a succeeded `source_info` `page_outlet`, else the outlet-table match of `input.source.url`, is non-empty.

#### Scenario: Migration of an existing database
- **WHEN** a database at the previous schema version is opened
- **THEN** it is migrated in place, its jobs are unchanged and they have no map values until the backfill ran

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
