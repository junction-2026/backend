## ADDED Requirements

### Requirement: Scraper configuration
The server SHALL read `SCRAPER_URL` (absolute http(s) base URL of the social-scraper-api service; unset or empty means no scraper is configured), `SCRAPER_TOKEN` (sent as `Authorization: Bearer <token>`) and `SCRAPER_TIMEOUT` (Go duration, default `120s`). None of the values SHALL be logged or exposed through any API response or event.

#### Scenario: No scraper configured
- **WHEN** `SCRAPER_URL` is unset
- **THEN** the server starts and `POST /api/v1/jobs` with `scrape` answers 503 `scrape_unavailable`

#### Scenario: Default timeout
- **WHEN** `SCRAPER_URL` and `SCRAPER_TOKEN` are set and `SCRAPER_TIMEOUT` is unset
- **THEN** scrapes are aborted after 120 seconds

### Requirement: Scraper configuration errors
A set `SCRAPER_URL` without `SCRAPER_TOKEN`, an invalid `SCRAPER_URL` or a non-positive or unparsable `SCRAPER_TIMEOUT` SHALL abort startup with a message naming the variable but not its value.

#### Scenario: Token required
- **WHEN** `SCRAPER_URL` is set and `SCRAPER_TOKEN` is empty
- **THEN** the process exits non-zero with a message naming `SCRAPER_TOKEN`

### Requirement: Scrape request tests
Table-driven handler tests SHALL cover the `scrape` member: both or neither of `input` and `scrape` (key `input`), an invalid or missing `scrape.url`, `scrape.max_posts` and `scrape.max_comments` of the wrong type or out of range, defaults in the created job and 503 `scrape_unavailable` without a created job.

#### Scenario: Suite passes
- **WHEN** `go test ./... -race -count=1` runs
- **THEN** the scrape request tests pass
