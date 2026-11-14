## MODIFIED Requirements

### Requirement: Applicability
Applicability SHALL be evaluated once from `applies_to` against the Input, at job creation (for a job created with `scrape`: once its input is mapped): `content_types` must contain the input's media type (case-insensitive, parameters ignored), `requires_source_url` requires a non-empty `source.url` and `min_content_length` is compared with the character count of `normalized_content`. Non-matching analyzers SHALL be `skipped` with `not_applicable`.

#### Scenario: Inapplicable analyzer
- **WHEN** analyzer `htmlonly` applies only to `text/html` and the job content is `text/plain`
- **THEN** it is `skipped` with `not_applicable`

#### Scenario: Scrape job applicability after mapping
- **WHEN** analyzer `long` has `min_content_length: 100` and a scrape job's mapped content has 10 characters
- **THEN** `long` is `pending` while the job is `scraping` and is `skipped` with `not_applicable` after `scrape.succeeded`

## ADDED Requirements

### Requirement: Scraper client
The package `internal/scraper` SHALL hold the HTTP client of the social-scraper-api: it posts `{"url", "max_posts", "max_comments"}` to `{SCRAPER_URL}/scrape` with `Authorization: Bearer {SCRAPER_TOKEN}` and decodes the ScrapeResult tolerantly, ignoring unknown members and values.

#### Scenario: Unknown members ignored
- **WHEN** a successful ScrapeResult has an extra member `lang`
- **THEN** the scrape succeeds and `source.metadata.scrape` still contains `lang`

### Requirement: Scraper error mapping
The scraper client SHALL pass the `code` and `details` of a scraper error envelope through, and SHALL give `scraper_unavailable` for connection errors, 401, `invalid_request`, `validation_failed`, 500 `internal_error` and bodies that are neither a valid ScrapeResult nor an error envelope, `timeout` when the scrape timeout ends the call and `cancelled` when the job is cancelled. Error messages SHALL NOT contain the scraper URL or token.

#### Scenario: Error passed through
- **WHEN** the scraper answers 403 `{"error": {"code": "account_private", "message": "private", "details": {"platform": "instagram"}}}`
- **THEN** the client returns `code: account_private` with `details.platform: instagram`

#### Scenario: Invalid body
- **WHEN** the scraper answers 200 with `[]`
- **THEN** the client returns `scraper_unavailable` with a message that contains neither the scraper URL nor the token

### Requirement: Scrape phase
For a job created with `scrape`, the engine SHALL run the scrape in the job's goroutine once the job holds a global job slot and before any analyzer is scheduled, under the job's cancellation context and `SCRAPER_TIMEOUT`. The job store SHALL keep the Scrape state on the job record and every scrape event SHALL be published through the job's event bus under the job lock, so REST reads and events agree. A scrape result that arrives after the job was cancelled SHALL be discarded.

#### Scenario: Cancel aborts the request
- **WHEN** a job is cancelled while the scraper request is in flight
- **THEN** the request's context is cancelled and the job ends `cancelled` with `scrape.error.code: cancelled`

#### Scenario: Timeout
- **WHEN** the scraper does not answer within `SCRAPER_TIMEOUT`
- **THEN** the job ends `failed` with `scrape.error.code: timeout`

### Requirement: Scrape engine tests
Engine tests SHALL use an `httptest` stub scraper and cover success for a post and a profile URL (input mapping), each error mapping, timeout, `no_text_content`, cancellation while scraping and the event order, and SHALL pass under the race detector.

#### Scenario: Suite passes
- **WHEN** `go test ./... -race -count=1` runs
- **THEN** the scrape engine tests pass
