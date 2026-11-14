## MODIFIED Requirements

### Requirement: Platform module interface and registry
Platform support SHALL be the Go interface `Module` with `Scrape(ctx, Target, Request) (*Result, error)`; `Target` holds `Platform`, `URLKind`, `URL *url.URL` and `IDs`. The registry SHALL be a map from platform to `Module`, built by `cmd/orchestrator` through `Registry.Register(platform, module)`, so adding a platform needs no edit of core files. URL recognition for `bluesky` and `mastodon` SHALL live in `internal/socialscraper/urls.go`. Typed failures SHALL be `*socialscraper.Error` values.

#### Scenario: Unrecognized URL
- **WHEN** no platform recognizes the URL
- **THEN** the scrape fails with `unsupported_url`

#### Scenario: Unexpected module failure
- **WHEN** a module returns an error that is not a typed scrape error
- **THEN** the scrape fails with `internal_error`, its message has no stack trace or credential and the error text appears only in the redacted log

#### Scenario: Registration from a platform file
- **WHEN** `cmd/orchestrator` registers a module for a platform
- **THEN** URLs of that platform reach its module and no core file is edited

### Requirement: Platforms without a module
URL recognition SHALL cover the platforms `bluesky` and `mastodon`. A recognized platform without a registered module SHALL fail with `platform_not_configured` and `details.platform`, never `internal_error`.

#### Scenario: Module not installed
- **WHEN** `https://mastodon.social/@alice` is scraped with a registry that has no Mastodon module
- **THEN** the scrape fails with `platform_not_configured` and `details.platform` `mastodon`

### Requirement: Scraper README
`README.md` SHALL have a "Scraping" section that describes in-process scraping, lists Bluesky and Mastodon as the only scraped platforms with their URL shapes, documents `SCRAPER_ENABLED`, `SCRAPER_TIMEOUT` and `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`, names the scrape error codes a job can end with, and has a migration note. It SHALL document no platform credential setting. No text in `README.md` or `docs/` SHALL mention Facebook, a browser binary, `cmd/scraper` or `scraper.Dockerfile`.

#### Scenario: Operator instructions
- **WHEN** an operator reads the "Scraping" section
- **THEN** it lists the three variables with defaults and the two supported platforms

#### Scenario: Error codes
- **WHEN** an operator reads how a scrape job fails
- **THEN** the README names the scrape error codes, including `unsupported_url`, `account_private` and `platform_not_configured`

#### Scenario: Migration note
- **WHEN** an operator upgrades from the two-container setup
- **THEN** the note tells them to remove the scraper container and its Traefik route, that `SCRAPER_URL`, `SCRAPER_TOKEN` and `SCRAPER_LISTEN_ADDR` are ignored, that only Bluesky and Mastodon are scraped and that the browser-based platform is no longer supported

### Requirement: Panic recovery
A panic in a platform module SHALL be recovered and returned as `internal_error` with a fixed message (surfaced to the job as `scraper_unavailable`). The stack trace SHALL appear only in the redacted orchestrator log, never in the error, and the orchestrator SHALL keep serving.

#### Scenario: Panicking module
- **WHEN** a module panics with a message containing the value of `SCRAPER_TOKEN` and a `Bearer` value
- **THEN** the scrape fails with `internal_error`, the error has no stack trace or secret value, the log line has the stack with the values redacted and the orchestrator keeps serving requests

### Requirement: Scraper logging and redaction
The orchestrator SHALL log JSON lines with `log/slog` through the redacting handler of `internal/socialscraper`, which replaces `Bearer <...>` values (including `Authorization: Bearer ...` headers) and the value of the secret environment variable `SCRAPER_TOKEN` with `[redacted]` in messages, attributes and panic traces. The redactor SHALL read secrets from the environment only and SHALL NOT read any file. Scrape error messages SHALL pass through the same redaction.

#### Scenario: Secret in an error
- **WHEN** a module error text contains the value of `SCRAPER_TOKEN`
- **THEN** the emitted log line contains `[redacted]` and not the value

#### Scenario: Sentinel token
- **WHEN** scrapes run with a sentinel `SCRAPER_TOKEN` value and succeed, fail and panic
- **THEN** no sentinel appears in any log line or scrape error

### Requirement: Scraper fixtures
Recorded fixtures SHALL live in `internal/socialscraper/<platform>/testdata/` for `bluesky` and `mastodon`.

#### Scenario: Fixture location
- **WHEN** the repository is checked out
- **THEN** each platform directory above has its fixtures in `testdata/` and no fixture directory exists for any other platform

### Requirement: In-process scraper runtime
The scraper core SHALL run inside the orchestrator process. At startup `cmd/orchestrator` SHALL build a platform registry with only the `bluesky` and `mastodon` modules, both using one shared `*http.Client` from `socialscraper.NewClient`, and SHALL pass an in-process scraper to the engine. A scrape SHALL call `socialscraper.Scraper.Scrape` directly: no HTTP request, no bearer token and no separate process are involved.

#### Scenario: Scrape without a scraper service
- **WHEN** the orchestrator starts without `SCRAPER_URL` and a job is created with `{"scrape": {"url": "https://bsky.app/profile/alice.bsky.social"}}`
- **THEN** the job's scrape runs in the orchestrator process and no request is sent to a scraper service

#### Scenario: One shared client
- **WHEN** the orchestrator builds the registry
- **THEN** the Bluesky and Mastodon modules use the same `*http.Client`, bounded by `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`, and no other platform has a module

### Requirement: In-process scraper configuration
For the platform modules the orchestrator SHALL read only `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` (default 30, a positive number, decimals allowed) from the environment. There SHALL be no platform credential setting. An invalid timeout SHALL abort startup with a message that names the variable and never its value. No setting value SHALL be logged; redaction is specified by "Scraper logging and redaction".

#### Scenario: Invalid timeout
- **WHEN** `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` is `abc`, `0` or `-3`
- **THEN** the orchestrator exits non-zero with a message that names the variable and does not contain the value

#### Scenario: Decimal timeout
- **WHEN** `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` is `2.5`
- **THEN** each outbound platform request is bounded by 2.5 seconds

#### Scenario: Default timeout
- **WHEN** `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` is unset
- **THEN** each outbound platform request is bounded by 30 seconds

#### Scenario: Authorization header in a log line
- **WHEN** a log message contains `Authorization: Bearer abc.def`
- **THEN** the emitted line contains `[redacted]` and not `abc.def`

## ADDED Requirements

### Requirement: Hosts excluded from Mastodon recognition
URL recognition SHALL exclude only `bsky.app` (with an optional `www.`, `m.` or `mobile.` prefix) from Mastodon recognition. Every other hostname that passes the hostname check SHALL be a Mastodon candidate when its path has a Mastodon shape (`/@<user>`, `/@<user>/<digits>`, `/users/<user>`, `/users/<user>/statuses/<digits>`), with no special case for any other host. A URL without a Bluesky or Mastodon shape SHALL fail with `unsupported_url` without any network request.

#### Scenario: Facebook page
- **WHEN** `https://www.facebook.com/examplenews` is scraped
- **THEN** the scrape fails with `unsupported_url` and no outbound request is made

#### Scenario: Short video host
- **WHEN** `https://fb.watch/abc123/` is scraped
- **THEN** the scrape fails with `unsupported_url` and no outbound request is made

#### Scenario: X status
- **WHEN** `https://x.com/alice/status/1790000000000000000` is scraped
- **THEN** the scrape fails with `unsupported_url` and no outbound request is made

#### Scenario: Instagram reel
- **WHEN** `https://www.instagram.com/reel/C1a2b3c4d5/` is scraped
- **THEN** the scrape fails with `unsupported_url` and no outbound request is made

#### Scenario: Mastodon-shaped path on a former platform host
- **WHEN** `https://www.threads.net/@zuck` is recognized
- **THEN** it is a Mastodon candidate with `url_kind` `profile` and host `threads.net`, the same as `https://example.com/@zuck`, and an HTML 404 or a redirect from that host gives `unsupported_url`

#### Scenario: Bluesky host never probed as Mastodon
- **WHEN** `https://bsky.app/@alice` is scraped
- **THEN** the scrape fails with `unsupported_url` and no outbound request is made

### Requirement: Closed-platform modules removed
`internal/socialscraper` SHALL contain no platform module package other than `bluesky` and `mastodon` (`scrapertest` is the test helper). `internal/socialscraper/errors.go` SHALL define helpers only for the error codes the core and these modules produce: `unsupported_url`, `account_not_found`, `post_not_found`, `account_private`, `rate_limited`, `platform_not_configured`, `upstream_error`, `upstream_timeout` and `internal_error`.

#### Scenario: Repository check
- **WHEN** the repository is checked out
- **THEN** the only subpackages of `internal/socialscraper` are `bluesky`, `mastodon` and `scrapertest`, and `go mod tidy` leaves `go.mod` and `go.sum` unchanged

## REMOVED Requirements

### Requirement: Facebook URLs unsupported
**Reason**: Replaced by "Hosts excluded from Mastodon recognition": no host other than `bsky.app` gets a special case any more (project#24).
**Migration**: None. Facebook URLs still fail with `unsupported_url` without a request, because none of them has a Bluesky or Mastodon shape; Mastodon-shaped paths on those hosts follow the generic Mastodon rules.
