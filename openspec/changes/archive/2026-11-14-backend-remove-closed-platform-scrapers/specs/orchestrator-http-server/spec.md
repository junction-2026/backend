## ADDED Requirements

### Requirement: Single-process scrape job tests
An end-to-end test SHALL start the whole orchestrator (`run`) with the scraper's transport routed to `scrapertest` fixtures, create scrape jobs through `POST /api/v1/jobs` and follow them until they are done, without any network access.

#### Scenario: Supported platforms
- **WHEN** jobs are created for Bluesky and Mastodon URLs served from fixtures
- **THEN** each job completes with the scraped content as its input

#### Scenario: Unsupported social network URLs
- **WHEN** jobs are created for `https://x.com/alice`, `https://x.com/alice/status/1790000000000000000`, `https://www.facebook.com/examplenews` and `https://www.facebook.com/profile.php?id=100000000000001`
- **THEN** each ends `failed` with `scrape.error.code` `unsupported_url` and no outbound request is made

#### Scenario: Panicking module
- **WHEN** a platform module panics during a job's scrape
- **THEN** the job ends `failed` with `scrape.error.code` `scraper_unavailable` and the server keeps answering requests

## REMOVED Requirements

### Requirement: Single-process scrape tests
**Reason**: Its scenarios listed platforms that are no longer supported (project#24); a MODIFIED block cannot drop scenarios, so it is replaced by "Single-process scrape job tests" with Bluesky/Mastodon jobs and an "Unsupported social network URLs" scenario.
**Migration**: None. The same end-to-end test covers the supported platforms, unsupported URLs and a panicking module.
