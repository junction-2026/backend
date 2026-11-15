## MODIFIED Requirements

### Requirement: Listing reads one page
`Store.Page` SHALL order jobs newest first by `created_at` with ties by `id`, honour the status filter, the language filter, the search query and the cursor, and, without a search query, decode no more jobs than it returns. `GET /api/v1/jobs` SHALL request at most one page plus one job.

#### Scenario: Large history
- **WHEN** 10000 jobs are stored and a page of 20 is requested without a search query
- **THEN** at most 21 jobs are decoded and the ordering index is used

#### Scenario: Language filter without decoding
- **WHEN** a page of 2 is requested with a language filter and more matching jobs exist
- **THEN** 2 jobs are decoded and the query uses the language index

## ADDED Requirements

### Requirement: Stored job language
Each stored job SHALL hold its derived job language: the value `adapter.JobSourceLanguage` (`job.Job.SourceLanguage`) gives for the job, a lower-case ISO 639-1 code, or NULL (Memory store: empty) when unknown. It SHALL be computed from the job whenever the job is written, so it is non-null only once the job's `translation` run has succeeded.

#### Scenario: Translation succeeds
- **WHEN** a job is saved with a `running` translation run and saved again after it succeeded with `source_language: "RU"`
- **THEN** it is listed for `unknown` after the first save and for `ru` after the second, in both stores

#### Scenario: Interrupted translation
- **WHEN** a job with a `running` translation run is interrupted and saved
- **THEN** its stored language is unknown

### Requirement: Language column migration
The SQLite schema SHALL gain, as one appended migration that leaves the released entries of `migrations` unchanged, a nullable `language` column on `jobs` and an index on `(public, language, created_at DESC, id)`.

#### Scenario: Previous schema version
- **WHEN** a database at schema version 4 is opened
- **THEN** it is migrated to version 5 and lists the same jobs as before

### Requirement: Language backfill
The language migration SHALL set `language` for every existing row in the same transaction, in batches of 500 rows, by decoding each stored job and applying `adapter.JobSourceLanguage`. A row without a succeeded `translation` run, with a non-code `source_language`, or whose stored job cannot be decoded SHALL get NULL.

#### Scenario: Populated database
- **WHEN** a version 4 database holds jobs with `source_language` `de`, `en` and null, a job without translation, one with a failed translation, a non-public job, an undecodable job and more rows than two batches
- **THEN** after opening, each row has the code its stored translation gives or NULL, and filtering by each language lists the matching public jobs

### Requirement: Language migration idempotence
The language migration and its backfill SHALL be idempotent: opening a migrated database SHALL change nothing, and a failure during the migration SHALL roll back the column, index, backfill and version together so that the next open retries cleanly.

#### Scenario: Failure mid-migration
- **WHEN** the backfill fails while a version 4 database is opened
- **THEN** opening fails, the database stays at version 4, and the next open migrates it

#### Scenario: Reopen
- **WHEN** a migrated database is opened again
- **THEN** its version and stored languages are unchanged

### Requirement: Listing language filter
`Store.Page` SHALL accept a language filter: a code (only jobs with that stored language), `unknown` (only jobs with an unknown language) or none. The SQLite store SHALL apply it in SQL and the Memory store on the language recorded at save, together with status, q and public-only, before the cursor and limit, so the limit and cursor count matching jobs only.

#### Scenario: Paging through a language
- **WHEN** jobs in several languages are stored and a client pages with a language, a status, a search query and limit 1 or 2
- **THEN** both stores return exactly the matching public jobs, newest first, across the pages
