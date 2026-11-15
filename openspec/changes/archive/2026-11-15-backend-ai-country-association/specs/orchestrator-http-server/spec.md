## MODIFIED Requirements

### Requirement: Propaganda map endpoints
The server SHALL serve `GET {basePath}/map/countries` and `GET {basePath}/map/countries/{country_code}/analyses` (shared `propaganda-map-api` as of shared-specs @ 24b7969236d03059dd6447481c27afd541fcac7e, change `ai-country-association`), with handlers in `internal/httpapi/map.go`. Other methods SHALL yield 405 `method_not_allowed`; errors use the api-conventions envelope. The handlers SHALL only read the store: no analyzer, scraper or network call, no job change.

#### Scenario: Aggregates
- **WHEN** two `RU` and one `DE` map analyses exist, each with one map country
- **THEN** `GET /api/v1/map/countries` is 200 with the `DE` then `RU` items and `analyses: 3`

#### Scenario: Wrong method
- **WHEN** a client sends `POST /api/v1/map/countries`
- **THEN** the status is 405 with `error.code` `method_not_allowed`

### Requirement: Map list items
Each item of the list SHALL carry `job_id`, `created_at`, `finished_at`, `status`, `country_code`, `country`, `countries`, `outlet` (null without map outlet), `source` (`url`, `""` without URL, and `title` or null), `techniques` (`strength` only when stored), `verdict` (null without final assessment) and a `snippet` (whitespace collapsed, at most 200 code points, U+2026 appended when cut).

#### Scenario: Snippet
- **WHEN** `input.content` is `"  Short\n\ntext "`
- **THEN** `snippet` is `"Short text"`

#### Scenario: Pasted text item
- **WHEN** a map analysis was created from pasted text with `"source": {}` and associated with `US`
- **THEN** its item in the `US` list has `outlet: null`, `source: {"url": "", "title": null}` and `countries: [{"country_code": "US", "country": "United States", "basis": ["content"]}]`

## ADDED Requirements

### Requirement: Distinct map analyses count
The top-level `analyses` of `GET {basePath}/map/countries` SHALL be the store's number of distinct map analyses, not the sum of the items.

#### Scenario: Multi-country analysis
- **WHEN** one map analysis has the map countries `DE` and `US` and another only `US`
- **THEN** the items are `DE` with `analyses: 1` and `US` with `analyses: 2`, and the top-level `analyses` is 2


### Requirement: Map list item countries
An item's `country_code` and `country` SHALL be the requested country (upper case) and its name among the job's map countries; `countries` SHALL be all map countries of the job in order, each with `country_code`, `country` and `basis`.

#### Scenario: Item in the second country's list
- **WHEN** a map analysis has the map countries `DE` then `US` and a client lists `US`
- **THEN** its item has `country_code: "US"` and `countries` with `DE` then `US`

