# scraper-service Specification

## Purpose
Backend-local structure of the social-media scraper service that implements the shared social-scraper-api: its runtime and container, configuration and secret handling, platform-module registry, the Bluesky and Mastodon modules, offline tests, CI and documentation.

## Requirements

### Requirement: Platform module interface and registry
Platform support SHALL be the Go interface `Module` with `Scrape(ctx, Target, Request) (*Result, error)`; `Target` holds `Platform`, `URLKind`, `URL *url.URL` and `IDs`. The registry SHALL be a map from platform to `Module`, built by `cmd/orchestrator` through `Registry.Register(platform, module)`, so adding a platform needs no edit of core files. URL recognition (safety checks, Bluesky, Mastodon, `web` fallback) SHALL live in `urls.go`. Typed failures SHALL be `*socialscraper.Error` values.

#### Scenario: Unrecognized URL
- **WHEN** the URL fails the safety checks of `Recognize` (not http(s), userinfo, over 2,000 characters, local host)
- **THEN** the scrape fails with `unsupported_url`; every other URL not claimed by `bluesky` or `mastodon` is a `web` page

#### Scenario: Unexpected module failure
- **WHEN** a module returns an error that is not a typed scrape error
- **THEN** the scrape fails with `internal_error`, its message has no stack trace or credential and the error text appears only in the redacted log

#### Scenario: Registration from a platform file
- **WHEN** `cmd/orchestrator` registers a module for a platform
- **THEN** URLs of that platform reach its module and no core file is edited

### Requirement: Platforms without a module
URL recognition SHALL cover the platforms `bluesky`, `mastodon` and `web`. A recognized platform without a registered module SHALL fail with `platform_not_configured` and `details.platform`, never `internal_error`.

#### Scenario: Web module not installed
- **WHEN** `https://www.dw.com/en/a/a-1` is scraped with a registry that has no web module
- **THEN** the scrape fails with `platform_not_configured` and `details.platform` `web`

#### Scenario: Module not installed
- **WHEN** `https://mastodon.social/@alice` is scraped with a registry that has no Mastodon module
- **THEN** the scrape fails with `platform_not_configured` and `details.platform` `mastodon`

### Requirement: Bluesky module
The Bluesky module SHALL call the XRPC methods `app.bsky.actor.getProfile`, `app.bsky.feed.getAuthorFeed` and `app.bsky.feed.getPostThread` of `https://public.api.bsky.app` over `net/http`, without an SDK or authentication. It SHALL fill the profile members from the actor profile and `posts` from the author feed (own posts, reposts skipped, newest first, following the cursor until `max_posts`). Error mapping is unchanged: 400 is not found, 429 `rate_limited`, anything else `upstream_error`.

#### Scenario: Profile with history
- **WHEN** a client scrapes `https://bsky.app/profile/alice.bsky.social` with `max_posts` 5 and the author feed has a repost among 6 posts over two pages
- **THEN** the response has 5 own posts, newest first, none of them the repost

#### Scenario: Profile not found
- **WHEN** the AppView answers 400 for the profile
- **THEN** the status is 404 `account_not_found` with `details.platform` `bluesky`

#### Scenario: No credentials sent
- **WHEN** the module calls the AppView
- **THEN** no `Authorization` header is sent

### Requirement: Bluesky post thread and partial results
For post URLs the Bluesky module SHALL fill `post` and `comments` from the post thread (replies flattened, oldest first, at most `max_comments`). A failed feed request SHALL become a `history_unavailable` or `history_truncated` warning, and a missing replies list a `comments_unavailable` warning.

#### Scenario: Thread comments
- **WHEN** a post URL's thread has nested replies and one deleted reply
- **THEN** `comments` has the remaining replies flattened and oldest first, each with `in_reply_to_id` naming its parent

#### Scenario: Feed failure
- **WHEN** the author feed request fails after the profile was fetched
- **THEN** the response is 200 with `posts: []` and a `history_unavailable` warning

### Requirement: Mastodon module
The Mastodon module SHALL use the REST API of the instance named in the URL over HTTPS: `/api/v1/accounts/lookup`, `/api/v1/statuses/{id}`, `/api/v1/statuses/{id}/context` and `/api/v1/accounts/{id}/statuses`. It SHALL follow `Link: rel=next` until `max_posts`, exclude boosts, convert `content` and `note` HTML with `socialscraper.PlainText`, and report `verified` as null. Its HTTP client SHALL NOT follow redirects (`CheckRedirect` returns `http.ErrUseLastResponse`).

#### Scenario: Link pagination
- **WHEN** the first statuses page has a `Link` header with `rel="next"` and `max_posts` is 4
- **THEN** the module requests the next page, returns 4 posts and stops

#### Scenario: Post comments
- **WHEN** a status URL is scraped and its context has descendants
- **THEN** `comments` are the descendants oldest first, capped at `max_comments`, and no context request is made when `max_comments` is 0

#### Scenario: HTML stripped
- **WHEN** a status has content `<p>Hello &amp; <a href="https://x.y">welcome</a></p><p>Bye</p>`
- **THEN** the Post `text` is `Hello & welcome\nBye`

### Requirement: Mastodon host safety
The Mastodon module SHALL contact only the host named in the scraped URL: it SHALL NOT recognize IP-literal or dotless hosts, SHALL NOT follow redirects and SHALL follow `Link: rel=next` only to the same scheme (`https`), host and port. A non-JSON answer, a redirect or a non-Mastodon 404 SHALL mean the host does not serve the Mastodon API: the module returns `unsupported_url` and `Scraper.Scrape` scrapes the URL as a `web` page in the same scrape (web-page-scraper "Mastodon probe fallback").

#### Scenario: Link to another host
- **WHEN** `rel="next"` points at a different host
- **THEN** that URL is not requested

#### Scenario: Link with another scheme or port
- **WHEN** `rel="next"` points at the same host over `http` or on port 8443
- **THEN** that URL is not requested

#### Scenario: IP literal
- **WHEN** a client scrapes `https://127.0.0.1/@alice`
- **THEN** the scrape fails with `unsupported_url` (a local host, rejected before any module) and no request is made

#### Scenario: Redirect
- **WHEN** the instance answers a request with a redirect
- **THEN** the redirect is not followed and the module returns `unsupported_url`, so the URL is scraped as a `web` page

#### Scenario: Not a Mastodon host
- **WHEN** `/api/v1/accounts/lookup` answers 404 with an HTML body
- **THEN** the module returns `unsupported_url`, so the URL is scraped as a `web` page

### Requirement: Mastodon malformed data
When the requested Mastodon account or status has an unexpected shape (a missing `id`, a wrong-typed member) the module SHALL answer 502 `upstream_error`, never 500. A history item or comment that does not parse SHALL be skipped and reported once with the warning `partial_parse`.

#### Scenario: Status without account id
- **WHEN** a status URL's status object has an `account` without `id`
- **THEN** the status is 502 `upstream_error` with `details.platform` `mastodon`

#### Scenario: One bad history item
- **WHEN** one item of a statuses page has a wrong-typed counter
- **THEN** the response is 200, the other posts are returned and `warnings` has one `partial_parse`

### Requirement: Offline scraper tests
Scraper tests SHALL run offline: package `internal/socialscraper/scrapertest` SHALL provide an `http.RoundTripper` that routes requests to per-host fixture handlers and fails the test on any unrouted host. Every platform module SHALL accept an injected `*http.Client`, and no test SHALL use `http.DefaultTransport` against a real host. Tests SHALL cover URL recognition (table-driven), error mapping, panic recovery, timeouts, warnings, redaction and each module's success path.

#### Scenario: No network
- **WHEN** `go test ./... -race -count=1` runs without network access
- **THEN** all tests pass

#### Scenario: Unrouted host
- **WHEN** a test makes a request to a host that has no route
- **THEN** the test fails

### Requirement: Scraper README
`README.md` SHALL have a "Scraping" section that describes in-process scraping, lists Bluesky, Mastodon and `web` pages with their URL routing, documents `SCRAPER_ENABLED`, `SCRAPER_TIMEOUT` and `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`, names the scrape error codes a job can end with, and has a migration note. The web settings are documented per web-page-scraper "Web README". No text in `README.md` or `docs/` SHALL mention Facebook, a browser binary, `cmd/scraper` or `scraper.Dockerfile`.

#### Scenario: Operator instructions
- **WHEN** an operator reads the "Scraping" section
- **THEN** it lists the three variables with defaults, the web settings and the supported platforms

#### Scenario: Error codes
- **WHEN** an operator reads how a scrape job fails
- **THEN** the README names the scrape error codes, including `unsupported_url`, `account_private` and `platform_not_configured`

#### Scenario: Migration note
- **WHEN** an operator upgrades from the two-container setup
- **THEN** the note tells them to remove the scraper container and its Traefik route, that `SCRAPER_URL`, `SCRAPER_TOKEN` and `SCRAPER_LISTEN_ADDR` are ignored, that other URLs are scraped as web pages through Oxylabs and that the browser-based platform is no longer supported

### Requirement: Upstream timeout and body cap
Every outbound `http.Client` of the platform modules SHALL have the `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` timeout and SHALL cap response bodies at 16 MiB; a deadline or network timeout error from a module SHALL become `upstream_timeout` with `details.platform`.

#### Scenario: Oversized answer
- **WHEN** a platform answers with a body of more than 16 MiB
- **THEN** reading the body fails and the scrape does not buffer it

#### Scenario: Module ignores its context
- **WHEN** a module keeps running past the scrape deadline without checking its context
- **THEN** the scrape ends at the deadline and the module's late result is discarded

#### Scenario: Deadline error from a module
- **WHEN** a module returns an error wrapping `context.DeadlineExceeded` while the scrape's own deadline has not passed
- **THEN** the scrape fails with `upstream_timeout` and `details.platform` set

### Requirement: Panic recovery
A panic in a platform module SHALL be recovered and returned as `internal_error` with a fixed message (surfaced to the job as `scraper_unavailable`). The stack trace SHALL appear only in the redacted orchestrator log, never in the error, and the orchestrator SHALL keep serving.

#### Scenario: Panicking module
- **WHEN** a module panics with a message containing the value of `SCRAPER_TOKEN` and a `Bearer` value
- **THEN** the scrape fails with `internal_error`, the error has no stack trace or secret value, the log line has the stack with the values redacted and the orchestrator keeps serving requests

### Requirement: Scraper logging and redaction
The orchestrator SHALL log JSON lines with `log/slog` through the redacting handler of `internal/socialscraper`, which replaces `Bearer <...>` values (including `Authorization: Bearer ...` headers) and the values of `SCRAPER_TOKEN` and the `OXYLABS_*` credentials with `[redacted]` in messages, attributes and panic traces. The redactor SHALL read secrets from the environment only and SHALL NOT read any file. Scrape error messages SHALL pass through the same redaction.

#### Scenario: Secret in an error
- **WHEN** a module error text contains the value of `SCRAPER_TOKEN`
- **THEN** the emitted log line contains `[redacted]` and not the value

#### Scenario: Sentinel token
- **WHEN** scrapes run with a sentinel `SCRAPER_TOKEN` value and succeed, fail and panic
- **THEN** no sentinel appears in any log line or scrape error

### Requirement: Plain-text normalization helper
`socialscraper.PlainText` SHALL use `golang.org/x/net/html` to remove tags, decode entities, turn `<br>` and `</p>` into `\n`, keep link text and trim the result.

#### Scenario: Shared spec example
- **WHEN** the input is `<p>Hello &amp; <a href="https://x.y">welcome</a></p><p>Bye</p>`
- **THEN** the output is `Hello & welcome\nBye`

### Requirement: Scraper fixtures
Recorded fixtures SHALL live in `internal/socialscraper/<platform>/testdata/` for `bluesky`, `mastodon` and `web` (`news_article.html`, `js_shell.html`, `login_wall.html`, `empty.html`, `markdown_only.md`).

#### Scenario: Fixture location
- **WHEN** the repository is checked out
- **THEN** each platform directory above has its fixtures in `testdata/` and no fixture directory exists for any other platform

### Requirement: In-process scraper runtime
The scraper core SHALL run inside the orchestrator process. At startup `cmd/orchestrator` SHALL build a platform registry with only the `bluesky`, `mastodon` and `web` modules (`web` has its own client, bounded per request), the first two using one shared `*http.Client` from `socialscraper.NewClient`, and SHALL pass an in-process scraper to the engine. A scrape SHALL call `socialscraper.Scraper.Scrape` directly: no HTTP request, no bearer token and no separate process are involved.

#### Scenario: Scrape without a scraper service
- **WHEN** the orchestrator starts without `SCRAPER_URL` and a job is created with `{"scrape": {"url": "https://bsky.app/profile/alice.bsky.social"}}`
- **THEN** the job's scrape runs in the orchestrator process and no request is sent to a scraper service

#### Scenario: One shared client
- **WHEN** the orchestrator builds the registry
- **THEN** the Bluesky and Mastodon modules use the same `*http.Client`, bounded by `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`, and no platform other than `web` has a module

### Requirement: Scrape and upstream timeouts
Each scrape SHALL run under the orchestrator's `SCRAPER_TIMEOUT` (default 360 s, see web-page-scraper), which yields `timeout` (analysis-engine "Scraper error mapping"), also when a module ignores its context. Every outbound `bluesky`, `mastodon` and unrendered `web` request SHALL be bounded by `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` (default 30, a positive number, decimals allowed), which yields `upstream_timeout` with `details.platform`.

#### Scenario: Slow platform request
- **WHEN** `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` is `0.05` and a platform request takes longer
- **THEN** the scrape fails with `upstream_timeout` and `details.platform` set

#### Scenario: Module ignores its context
- **WHEN** a module keeps running past `SCRAPER_TIMEOUT` without checking its context
- **THEN** the scrape fails with `timeout` at the deadline

### Requirement: Standalone scraper removed
The repository SHALL NOT contain `cmd/scraper`, `scraper.Dockerfile`, `internal/socialscraper/facebook` or the HTTP server, bearer authentication, request-body decoding and health-check code that only served the standalone `POST /scrape` and `GET /health`. `go.mod` and `go.sum` SHALL NOT list the browser automation dependencies the Facebook module needed. The orchestrator `Dockerfile` SHALL be the only image.

#### Scenario: Repository check
- **WHEN** the repository is checked out
- **THEN** `cmd/scraper`, `scraper.Dockerfile` and `internal/socialscraper/facebook` do not exist and `go mod tidy` leaves `go.mod` and `go.sum` unchanged

### Requirement: In-process scraper configuration
For the Bluesky and Mastodon modules the orchestrator SHALL read only `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` (default 30, a positive number, decimals allowed) from the environment; they need no credential. The web module's settings are in web-page-scraper "Web page configuration". An invalid timeout SHALL abort startup with a message that names the variable and never its value. No setting value SHALL be logged; redaction is specified by "Scraper logging and redaction".

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

### Requirement: Hosts excluded from Mastodon recognition
URL recognition SHALL exclude only `bsky.app` (with an optional `www.`, `m.` or `mobile.` prefix) from Mastodon recognition. Every other hostname that passes the hostname check SHALL be a Mastodon candidate when its path has a Mastodon shape (`/@<user>`, `/@<user>/<digits>`, `/users/<user>`, `/users/<user>/statuses/<digits>`), with no special case for any other host. A URL without a Bluesky or Mastodon shape SHALL be a `web` page, unless it fails the safety checks (`unsupported_url`).

#### Scenario: Facebook page
- **WHEN** `https://www.facebook.com/examplenews` is recognized
- **THEN** it is a `web` page and no network request is made by recognition

#### Scenario: Short video host
- **WHEN** `https://fb.watch/abc123/` is recognized
- **THEN** it is a `web` page

#### Scenario: X status
- **WHEN** `https://x.com/alice/status/1790000000000000000` is recognized
- **THEN** it is a `web` page

#### Scenario: Instagram reel
- **WHEN** `https://www.instagram.com/reel/C1a2b3c4d5/` is recognized
- **THEN** it is a `web` page

#### Scenario: Mastodon-shaped path on a former platform host
- **WHEN** `https://www.threads.net/@zuck` is recognized
- **THEN** it is a Mastodon candidate with `url_kind` `profile` and host `threads.net`, the same as `https://example.com/@zuck`, and an HTML 404 or a redirect from that host makes it a `web` page in the same scrape

#### Scenario: Bluesky host never probed as Mastodon
- **WHEN** `https://bsky.app/@alice` is scraped
- **THEN** no Mastodon probe is made and the URL is a `web` page

### Requirement: Closed-platform modules removed
`internal/socialscraper` SHALL contain no platform module package other than `bluesky`, `mastodon` and `web` (`scrapertest` is the test helper). `internal/socialscraper/errors.go` SHALL define helpers only for the error codes the core and these modules produce: `unsupported_url`, `account_not_found`, `post_not_found`, `account_private`, `rate_limited`, `platform_not_configured`, `upstream_error`, `upstream_timeout`, `login_required` (web only), `no_text_content` and `internal_error`.

#### Scenario: Repository check
- **WHEN** the repository is checked out
- **THEN** the only subpackages of `internal/socialscraper` are `bluesky`, `mastodon`, `web` and `scrapertest`, and `go mod tidy` leaves `go.mod` and `go.sum` unchanged
