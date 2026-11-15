## MODIFIED Requirements

### Requirement: Listing reads one page
`Store.Page` SHALL order jobs newest first by `created_at` with ties by `id`, honour the status filter, the search query and the cursor, and, without a search query, decode no more jobs than it returns. `GET /api/v1/jobs` SHALL request at most one page plus one job.

#### Scenario: Large history
- **WHEN** 10000 jobs are stored and a page of 20 is requested without a search query
- **THEN** at most 21 jobs are decoded and the ordering index is used

## ADDED Requirements

### Requirement: Listing search before paging
`Store.Page` SHALL accept a search query and apply it, together with the status filter and `public = 1`, before the limit and the cursor, in both the Memory and the SQLite store, so both return identical results for the same data. A non-empty search SHALL NOT change the ordering (`created_at` DESC, `id`) or the cursor format.

#### Scenario: Store parity
- **WHEN** the same public and non-public jobs are stored in both stores and every combination of status, search and cursor is paged with limit 2
- **THEN** both stores return the same pages, and no page holds a non-public job

#### Scenario: Match beyond the limit
- **WHEN** 25 public jobs are stored, only the oldest matches, and a page of 21 is requested with that search
- **THEN** the page holds exactly that job

#### Scenario: After a restart
- **WHEN** the SQLite store is closed and reopened
- **THEN** a search finds the persisted matching job

### Requirement: Listing search without migration
The SQLite search SHALL NOT add a schema migration or column; it SHALL evaluate matching in Go, scanning the ordered rows and decoding them until `limit` matches are found or the rows end.

#### Scenario: Unchanged schema
- **WHEN** the store opens a database
- **THEN** the migration list is the one without search

### Requirement: Listing search matching
Matching SHALL compare both sides after Unicode lowercase mapping (`strings.ToLower`) and test literal substring containment in `id`, `input.content` (input not null) and `scrape.url` (scrape not null); SQLite `lower()` and `LIKE` SHALL NOT be relied on.

#### Scenario: Non-ASCII case
- **WHEN** a job has content "ÄRZTE warnen" and the search is "ärzte"
- **THEN** it matches

#### Scenario: Literal wildcards
- **WHEN** the search is `%` or `_` and no id, content or URL contains it
- **THEN** nothing matches
