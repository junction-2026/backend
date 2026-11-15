## ADDED Requirements

### Requirement: Job list language parameter
The list handler SHALL trim leading and trailing white space from `language` and lower-case it; an empty value SHALL be no filter; `unknown` or a value matching `^[a-z]{2}$` SHALL be passed to the store as the language filter; any other value SHALL yield 400 `invalid_request`.

#### Scenario: Case and white space
- **WHEN** a client calls `GET /api/v1/jobs?language=%20DE%20`
- **THEN** the body equals that of `GET /api/v1/jobs?language=de`

#### Scenario: Malformed value
- **WHEN** `language` is `deu`, `de-AT`, `german`, `d` or `null`
- **THEN** the status is 400 with `error.code` `invalid_request`

#### Scenario: Blank value
- **WHEN** a client calls `GET /api/v1/jobs?language=%20`
- **THEN** the body equals that of `GET /api/v1/jobs`

### Requirement: JobSummary language member
`summarize` SHALL set `language` to the job language (`adapter.JobSourceLanguage`) or null when unknown, and the member SHALL always be present in the JSON.

#### Scenario: Unknown language
- **WHEN** a listed job has no succeeded `translation` run
- **THEN** its item has `"language":null`
