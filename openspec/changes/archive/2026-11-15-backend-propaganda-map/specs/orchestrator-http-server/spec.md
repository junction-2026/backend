## ADDED Requirements

### Requirement: Propaganda map endpoints
The server SHALL serve `GET {basePath}/map/countries` and `GET {basePath}/map/countries/{country_code}/analyses` (shared change `propaganda-map-api`, shared-specs @ fa2a80df8674ccbbc6183931e95dbb7d7c8c5b85), registered in `httpapi.go` with their handlers in `internal/httpapi/map.go`. Other methods SHALL yield 405 `method_not_allowed` and errors SHALL use the api-conventions envelope. The handlers SHALL only read the store: no analyzer, scraper or network call and no job change.

#### Scenario: Aggregates
- **WHEN** two `RU` and one `DE` map analyses exist
- **THEN** `GET /api/v1/map/countries` is 200 with the `DE` then `RU` items and `analyses: 3`

#### Scenario: Wrong method
- **WHEN** a client sends `POST /api/v1/map/countries`
- **THEN** the status is 405 with `error.code` `method_not_allowed`

### Requirement: Map list parameters
The list handler SHALL accept `country_code` of exactly two ASCII letters (upper-cased), `limit` 1-100 (default 20) and an opaque cursor; any other value SHALL yield 400 `invalid_request`. A well-formed code without map analyses SHALL yield 200 `{"items": [], "next_cursor": null}`.

#### Scenario: Paging
- **WHEN** `RU` has 25 map analyses
- **THEN** the first page has 20 items and a cursor, the second 5 and `next_cursor: null`

#### Scenario: Malformed code
- **WHEN** a client requests `/api/v1/map/countries/DEU/analyses` or `/1A/analyses` or `?limit=0`
- **THEN** the status is 400 with `error.code` `invalid_request`

### Requirement: Map list items
Each item of the list SHALL carry `job_id`, `created_at`, `finished_at`, `status`, `country_code`, `country`, `outlet`, `source` (`url`, `title` or null), `techniques` (`strength` only when stored), `verdict` (null without final assessment) and a `snippet` (whitespace collapsed, at most 200 code points, U+2026 appended when cut).

#### Scenario: Snippet
- **WHEN** `input.content` is `"  Short\n\ntext "`
- **THEN** `snippet` is `"Short text"`
