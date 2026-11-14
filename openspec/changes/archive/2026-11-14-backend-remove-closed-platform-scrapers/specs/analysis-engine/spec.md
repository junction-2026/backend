## MODIFIED Requirements

### Requirement: Scraper error mapping
The adapter SHALL pass the `code`, message (redacted) and `details` of a scraper error through, and SHALL give `scraper_unavailable` for `internal_error` (including a panic in a platform module) and for a result that is not a valid ScrapeResult (missing `platform` or `url_kind`, or `url_kind: post` without `post`), `timeout` when the scrape timeout ends the call and `cancelled` when the job is cancelled. Error messages SHALL NOT contain credentials.

#### Scenario: Error passed through
- **WHEN** the core fails with `account_private` and `details.platform` `mastodon`
- **THEN** the adapter returns `code: account_private` with `details.platform: mastodon`

#### Scenario: Panic
- **WHEN** a platform module panics
- **THEN** the adapter returns `scraper_unavailable` and the process keeps running

#### Scenario: Invalid body
- **WHEN** a module returns a result with `url_kind: post` and no `post`
- **THEN** the adapter returns `scraper_unavailable`
