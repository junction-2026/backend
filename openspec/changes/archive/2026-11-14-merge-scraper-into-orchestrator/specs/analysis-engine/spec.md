## MODIFIED Requirements

### Requirement: Scraper client
The package `internal/scraper` SHALL be the engine's in-process adapter of the scraper core: it SHALL call `socialscraper.Scraper.Scrape` directly with the job's `url`, `max_posts` and `max_comments` (no HTTP request, no bearer token) and SHALL map the ScrapeResult to the job Input as before, keeping the ScrapeResult JSON in `source.metadata.scrape`.

#### Scenario: Unknown members ignored
- **WHEN** the core returns a ScrapeResult with members the mapping does not use, such as `bio` and `followers`
- **THEN** the scrape succeeds and `source.metadata.scrape` still contains them

#### Scenario: Post mapped to input
- **WHEN** the core returns a Mastodon post result with text `Vote no!` and a warning
- **THEN** the job input has content `Vote no!`, `source.platform` `mastodon` and the scrape has the warning

### Requirement: Scraper error mapping
The adapter SHALL pass the `code`, message (redacted) and `details` of a scraper error through, and SHALL give `scraper_unavailable` for `internal_error` (including a panic in a platform module) and for a result that is not a valid ScrapeResult (missing `platform` or `url_kind`, or `url_kind: post` without `post`), `timeout` when the scrape timeout ends the call and `cancelled` when the job is cancelled. Error messages SHALL NOT contain credentials.

#### Scenario: Error passed through
- **WHEN** the core fails with `account_private` and `details.platform` `instagram`
- **THEN** the adapter returns `code: account_private` with `details.platform: instagram`

#### Scenario: Panic
- **WHEN** a platform module panics
- **THEN** the adapter returns `scraper_unavailable` and the process keeps running

#### Scenario: Invalid body
- **WHEN** a module returns a result with `url_kind: post` and no `post`
- **THEN** the adapter returns `scraper_unavailable`

### Requirement: Scrape phase
For a job created with `scrape`, the engine SHALL run the scrape in the job's goroutine once the job holds a global job slot and before any analyzer is scheduled, under the job's cancellation context and `SCRAPER_TIMEOUT`. The job store SHALL keep the Scrape state on the job record and every scrape event SHALL be published through the job's event bus under the job lock, so REST reads and events agree. A scrape result that arrives after the job was cancelled SHALL be discarded.

#### Scenario: Cancel aborts the request
- **WHEN** a job is cancelled while its scrape is running
- **THEN** the scrape's context is cancelled and the job ends `cancelled` with `scrape.error.code: cancelled`

#### Scenario: Timeout
- **WHEN** the scrape does not finish within `SCRAPER_TIMEOUT`
- **THEN** the job ends `failed` with `scrape.error.code: timeout`

### Requirement: Scrape engine tests
Engine and adapter tests SHALL use an in-process scraper core with scripted platform modules and cover success for a post and a profile URL (input mapping), each error mapping including panic, invalid result, timeout and cancellation, `no_text_content` and the event order, and SHALL pass under the race detector.

#### Scenario: Suite passes
- **WHEN** `go test ./... -race -count=1` runs
- **THEN** the scrape engine and adapter tests pass
