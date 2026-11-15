## MODIFIED Requirements

### Requirement: Map values schema
The SQLite schema SHALL gain, as appended migrations that leave released migrations unchanged, per-job map values: `map_eligible` (NULL until determined), `map_outlet` (JSON, null without map outlet), `map_techniques` (JSON list of name, display and stored strength), `map_countries` (JSON list with `country_code`, `country`, `basis`) and a table `job_map_countries` with one row per map country of an eligible job, indexed by `(country_code, created_at DESC, job_id)`.

#### Scenario: Migration of an existing database
- **WHEN** a database at the previous schema version is opened
- **THEN** it is migrated in place, its jobs are unchanged and they have no map values until the backfill ran

### Requirement: Map analysis eligibility
A job SHALL be a map analysis (`map_eligible` 1) exactly when it is done and public, its `techniques` run succeeded with at least one known detected technique (weak included), and it has at least one map country. The map outlet SHALL be the `page_outlet` of a succeeded `source_info` run when it is not null, otherwise the outlet-table match of `input.source.url`. A done public job with detected propaganda but no map country SHALL keep its map outlet and technique set with `map_eligible` 0.

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
- **WHEN** `page_outlet.country_code` is `""` and `country_association` has no countries
- **THEN** the job has no map country and is not eligible, without trying the outlet table

#### Scenario: Unmatched URL
- **WHEN** `input.source.url` is `https://unknown-blog.fr/post`, no outlet matches and `country_association` has no countries
- **THEN** the job is not eligible

### Requirement: Map values fixed when done
The map values SHALL be computed once, when a job is saved with `done` true, and written in the same transaction as the done record. They SHALL never be recomputed, also when the outlet table changes or the job is saved again, except by "Country association backfill storage", which determines the map countries again with the stored map outlet and technique set.

#### Scenario: Outlet table replaced later
- **WHEN** a done job was placed in `GB` by the outlet table and the table later lists its domain under `IE`
- **THEN** the job stays in `GB`, also after the backfill set its association

### Requirement: Map queries
The store SHALL answer an aggregate query (per map country code: the number of eligible jobs having it, the name of the newest, per-technique counts by count descending then technique-name order; plus the number of distinct eligible jobs) and a list query per code (eligible jobs having it, each once, newest first by `(created_at, id)`, keyset cursor, optional limit, with all map countries). A job removed by history retention or `Prune` SHALL leave both answers.

#### Scenario: Retention
- **WHEN** `Prune` deletes a done map analysis of `PL`
- **THEN** the `PL` aggregate and list no longer contain it and no map data of it remains

#### Scenario: Two countries
- **WHEN** the only map analysis has the map countries `RU` and `UA`
- **THEN** both aggregates count it once, both lists hold it once and the distinct number is 1

### Requirement: Map backfill
At startup (`Engine.Recover`) every stored done job without map values SHALL get them from its stored analyzer results, input and `country_association`, with the outlet table loaded at that time, in batches of at most 200 inside transactions. The backfill SHALL be idempotent. A failure SHALL be logged and retried at the next start and SHALL NOT prevent the server from serving or creating jobs.

#### Scenario: Job stored before the map
- **WHEN** a database holding a done job with detected techniques and a `page_outlet` in `FI` is opened and recovered
- **THEN** the map aggregate counts the job for `FI`

## ADDED Requirements

### Requirement: Single map country migration
Migration 4 SHALL keep the `map_country_code` and `map_country` of each existing eligible job as its only map country with basis `outlet`; those two columns are no longer read.

#### Scenario: Existing map country kept
- **WHEN** a database at schema version 3 holds an eligible job with `map_country_code` `FI`
- **THEN** after migration the job is listed for `FI` with map countries `[FI, basis outlet]`

### Requirement: Map countries of a job
The map countries SHALL be the map outlet's `country_code` (upper-cased, only when it is two ASCII letters) with basis `outlet`, followed by the countries of a `succeeded` `country_association` with basis `content`; a code given by both appears once, first, with both bases. The map country is no longer limited to the outlet's country.

#### Scenario: Pasted text associated with the US
- **WHEN** a done public job without source URL has detected techniques and `country_association.countries` `[US]`
- **THEN** it is eligible with map countries `[US, basis content]` and no map outlet

#### Scenario: Outlet and content merged
- **WHEN** the page outlet is in `DE` and `country_association.countries` is `[US, DE]`
- **THEN** the map countries are `DE` (bases `outlet`, `content`) then `US` (basis `content`)

### Requirement: Job country association persistence
Both stores SHALL persist and return `Job.country_association` as part of the stored Job JSON; a job stored without the member SHALL read as null.

#### Scenario: Old row
- **WHEN** a job stored before this change is read
- **THEN** its `country_association` is null

### Requirement: Country association backfill candidates
The SQLite store SHALL count backfill attempts per job (`country_attempts`) and return as candidates, fewest attempts first then newest, the done public jobs whose `techniques` run succeeded with a non-empty `detected`, whose `country_association` is absent, null or `failed` and that had fewer than 3 attempts. The Memory store has no history and SHALL return no candidates.

#### Scenario: Not a candidate
- **WHEN** a stored done job has `detected: []`, or `public` false
- **THEN** it is never a candidate

### Requirement: Country association backfill storage
Storing a backfill result SHALL count an attempt and, only while the job's association is still absent, null or `failed`, set it in the job JSON and on the live record of the working set (under its lock), and replace the job's map columns and `job_map_countries` rows in the same transaction. A `succeeded` or `skipped` association SHALL never be changed.

#### Scenario: Settled meanwhile
- **WHEN** a result is stored for a job whose association is already `succeeded`
- **THEN** the job is unchanged
