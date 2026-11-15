## ADDED Requirements

### Requirement: Job list search parameter
The list handler SHALL trim leading and trailing white space from `q`, reject a trimmed `q` of more than 200 code points with 400 `invalid_request`, and pass an empty `q` as no filter.

#### Scenario: Blank query
- **WHEN** a client calls `GET /api/v1/jobs?q=%20%20`
- **THEN** the body equals that of `GET /api/v1/jobs`

#### Scenario: Limit after trimming
- **WHEN** `q` is 200 code points surrounded by spaces
- **THEN** the status is 200, and with 201 code points it is 400 `invalid_request`

### Requirement: JobSummary excerpt construction
`summarize` SHALL set `excerpt` to the first 200 code points of `input.content` when it is non-empty, else to the first 200 code points of `scrape.url` when `scrape` is not null, else null, cutting on rune boundaries and copying verbatim.

#### Scenario: Multi-byte cut
- **WHEN** `input.content` has 500 code points and the 200th is a multi-byte character
- **THEN** `excerpt` ends with that whole character

#### Scenario: Stored before the change
- **WHEN** jobs are stored without an `excerpt` and served after a restart
- **THEN** each item has `excerpt` built from its stored content or scrape URL, or null
