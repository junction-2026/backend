## ADDED Requirements

### Requirement: X module registration
`cmd/orchestrator` SHALL register the X module for `twitter` when it builds the scraper registry at startup, passing the shared `*http.Client` and the process environment for `TWITTER_AUTH_TOKEN` and `TWITTER_CT0`.

#### Scenario: Registered
- **WHEN** the orchestrator starts with scraping enabled
- **THEN** the registry has a module for `twitter` and none for any platform outside `bluesky`, `mastodon`, `instagram`, `threads` and `twitter`

### Requirement: Offline X module tests
Tests of the X module SHALL use the fixtures under `internal/socialscraper/x/testdata/` and routed in-memory HTTP; no test SHALL contact the network.

#### Scenario: Routed requests only
- **WHEN** the X module tests run
- **THEN** every request is served by the `scrapertest` router from fixtures

## MODIFIED Requirements

### Requirement: X credentials not configured
When either `TWITTER_AUTH_TOKEN` or `TWITTER_CT0` is unset or empty, the X module SHALL fail with `platform_not_configured` with `details.platform` `twitter` and `details.missing_settings` naming exactly the unset variables, before any request to X is made. Other platforms SHALL be unaffected.

#### Scenario: One variable unset
- **WHEN** `TWITTER_CT0` is unset and `https://x.com/alice` is scraped
- **THEN** the scrape fails with `platform_not_configured` and `details.missing_settings` `["TWITTER_CT0"]`, no variable value is in the error and no request was sent to X

### Requirement: X error mapping
A failed history or replies request SHALL become a `history_unavailable` or `comments_unavailable` warning. A missing user or tweet SHALL fail with `account_not_found` or `post_not_found`, a protected account with `account_private`, a deadline with `upstream_timeout`, and any other failure, including 401 or 403 for rejected cookies, with `upstream_error` and a fixed message.

#### Scenario: Protected account
- **WHEN** the looked-up user is protected
- **THEN** the scrape fails with `account_private` and `details.platform` `twitter`

#### Scenario: Replies fail
- **WHEN** the request for more replies fails after the tweet was fetched
- **THEN** the scrape succeeds with `comments: []` and a `comments_unavailable` warning

### Requirement: Credentials never exposed
The values of `TWITTER_AUTH_TOKEN` and `TWITTER_CT0` SHALL NOT appear in any scrape result, scrape error, job, event, API response, log line or captured output, including when an upstream error or a panic carries them; errors SHALL carry fixed messages and Go error type names only, never upstream error text.

#### Scenario: Sentinel credentials
- **WHEN** both variables hold sentinel strings and X scrapes succeed, fail and panic with messages containing the sentinels
- **THEN** neither sentinel appears in any scrape result, error or log record

### Requirement: X rate limiting
HTTP 429 from X SHALL fail the scrape with `rate_limited`, with `details.retry_after_s` derived from the `x-rate-limit-reset` header when present.

#### Scenario: Throttled
- **WHEN** X answers 429 with `x-rate-limit-reset` 90 seconds ahead
- **THEN** the scrape fails with `rate_limited` and `details.retry_after_s` 90

## REMOVED Requirements

### Requirement: Facebook browser module
**Reason**: Facebook is not supported (shared change `merge-scraper-drop-facebook`); the module and its browser are deleted.
**Migration**: Facebook URLs fail with `unsupported_url`.

### Requirement: Facebook personal profiles refused
**Reason**: No Facebook module; `unsupported_account_type` is no longer produced.
**Migration**: Facebook profile URLs fail with `unsupported_url`.

### Requirement: Facebook missing or unreadable pages
**Reason**: No Facebook module.
**Migration**: None.

### Requirement: Image carries chromium
**Reason**: No browser is needed; the orchestrator image is the only image.
**Migration**: None.

### Requirement: Facebook browser abstraction
**Reason**: No Facebook module.
**Migration**: None.

### Requirement: Facebook and X registration
**Reason**: `cmd/scraper` is removed and Facebook is not registered; replaced by "X module registration".
**Migration**: None.

### Requirement: Facebook scrape timeout
**Reason**: No Facebook module.
**Migration**: None.

### Requirement: Offline module tests
**Reason**: Covered Facebook's fake browser; replaced by "Offline X module tests".
**Migration**: None.
