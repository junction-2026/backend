## ADDED Requirements

### Requirement: Ignored scraper variables
`SCRAPER_URL`, `SCRAPER_TOKEN` and `SCRAPER_LISTEN_ADDR` of the former standalone scraper SHALL be ignored. When one of them is set, startup SHALL log one warning that names the variable and never its value, and SHALL continue.

#### Scenario: Old env file
- **WHEN** the orchestrator starts with `SCRAPER_URL=http://scraper:8003` and `SCRAPER_TOKEN=s3cret`
- **THEN** it starts, scrapes in process, logs one warning naming `SCRAPER_URL` and one naming `SCRAPER_TOKEN`, and no log line contains `http://scraper:8003` or `s3cret`

### Requirement: Single-process scrape tests
An end-to-end test SHALL start the whole orchestrator (`run`) with the scraper's transport routed to `scrapertest` fixtures, create scrape jobs through `POST /api/v1/jobs` and follow them until they are done, without any network access.

#### Scenario: Supported platforms
- **WHEN** jobs are created for Bluesky, Mastodon, Instagram, Threads and X URLs served from fixtures
- **THEN** each job completes with the scraped content as its input

#### Scenario: Facebook URLs
- **WHEN** jobs are created for `https://www.facebook.com/examplenews` and `https://www.facebook.com/profile.php?id=100000000000001`
- **THEN** both end `failed` with `scrape.error.code` `unsupported_url` and no outbound request is made

#### Scenario: Panicking module
- **WHEN** a platform module panics during a job's scrape
- **THEN** the job ends `failed` with `scrape.error.code` `scraper_unavailable` and the server keeps answering requests

### Requirement: Scraper setting errors
A `SCRAPER_ENABLED` value other than `true` or `false` (in any case), a non-positive or unparsable `SCRAPER_TIMEOUT` or an invalid `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` SHALL abort startup with a message naming the variable but not its value.

#### Scenario: Invalid switch
- **WHEN** `SCRAPER_ENABLED` is `yes-secret`
- **THEN** the process exits non-zero with a message naming `SCRAPER_ENABLED` and not containing `yes-secret`

## MODIFIED Requirements

### Requirement: Scraper configuration
Scraping SHALL be enabled by default. The server SHALL read `SCRAPER_ENABLED` (`true` or `false`, case-insensitive, default `true`), `SCRAPER_TIMEOUT` (Go duration, default `120s`, the bound of one scrape) and the scraper settings of scraper-service "In-process scraper configuration". With `SCRAPER_ENABLED=false`, `POST /api/v1/jobs` with `scrape` SHALL answer 503 `scrape_unavailable` and create no job. None of the values SHALL be logged or exposed through any API response or event.

#### Scenario: Enabled by default
- **WHEN** the server starts without any scraper variable
- **THEN** `POST /api/v1/jobs` with `{"scrape": {"url": "https://bsky.app/profile/alice.bsky.social"}}` answers 201

#### Scenario: No scraper configured
- **WHEN** `SCRAPER_ENABLED` is `FALSE`
- **THEN** the server starts and `POST /api/v1/jobs` with `scrape` answers 503 `scrape_unavailable` and no job is created

#### Scenario: Default timeout
- **WHEN** `SCRAPER_TIMEOUT` is unset
- **THEN** scrapes are aborted after 120 seconds

### Requirement: Scrape request tests
Table-driven handler tests SHALL cover the `scrape` member: both or neither of `input` and `scrape` (key `input`), an invalid or missing `scrape.url`, `scrape.max_posts` and `scrape.max_comments` of the wrong type or out of range, defaults in the created job and 503 `scrape_unavailable` without a created job when scraping is disabled.

#### Scenario: Suite passes
- **WHEN** `go test ./... -race -count=1` runs
- **THEN** the scrape request tests pass

## REMOVED Requirements

### Requirement: Scraper configuration errors
**Reason**: `SCRAPER_URL` and `SCRAPER_TOKEN` are no longer read; replaced by "Scraper setting errors" and "Ignored scraper variables".
**Migration**: Remove `SCRAPER_URL` and `SCRAPER_TOKEN` from deployments; leaving them set only logs a warning.
