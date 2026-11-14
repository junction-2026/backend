## ADDED Requirements

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
