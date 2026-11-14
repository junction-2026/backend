## REMOVED Requirements

### Requirement: Scraper service runtime and image
**Reason**: The service is rebuilt as a Go binary; the earlier runtime, its `scraper/` directory and the image
packaging that went with it (including the `git` package in the image) no longer exist.
**Migration**: The runtime is specified by "Scraper runtime", the image by "Scraper image"; `scraper/Dockerfile`
is replaced by `scraper.Dockerfile` at the repository root.

## ADDED Requirements

### Requirement: Scraper runtime
`cmd/scraper` SHALL be a Go binary of the backend module that serves the shared social-scraper-api with `net/http` on `SCRAPER_LISTEN_ADDR` (default `0.0.0.0:8003`). On SIGTERM it SHALL stop accepting requests and finish in-flight ones within 10 seconds.

#### Scenario: Default address
- **WHEN** the binary starts with `SCRAPER_TOKEN` set and no `SCRAPER_LISTEN_ADDR`
- **THEN** `GET /health` on port 8003 answers 200

#### Scenario: Graceful shutdown
- **WHEN** the process receives SIGTERM while idle
- **THEN** it exits with status 0 within 10 seconds

### Requirement: Health check command
`scraper -healthcheck` SHALL request `http://127.0.0.1:<port>/health`, where the port is that of `SCRAPER_LISTEN_ADDR` (default 8003), and SHALL exit 0 on status 200 and 1 on any other outcome. It SHALL NOT require `SCRAPER_TOKEN`.

#### Scenario: Healthy server
- **WHEN** a scraper listens on the port and `scraper -healthcheck` runs
- **THEN** the exit status is 0

#### Scenario: No server
- **WHEN** nothing listens on the port
- **THEN** the exit status is 1

### Requirement: Scraper authentication handling
The service SHALL compare the bearer token with the configured token in constant time (`crypto/subtle`) and SHALL answer an authentication failure with 401 before it reads the request body.

#### Scenario: Body not read on failure
- **WHEN** a request without a valid token has a body that cannot be read
- **THEN** the status is 401 and the body was never read

### Requirement: Scrape body decoding
The service SHALL decode the request body into `json.RawMessage` members and check each one's JSON type: `url` MUST be a JSON string, `max_posts` (1–100) and `max_comments` (0–200) MUST be JSON numbers with an integral value. Booleans, strings, null and numbers such as `1.5` SHALL be rejected, and every offending member SHALL appear in `details.fields` of the 422 `validation_failed` answer. Unknown members SHALL be ignored.

#### Scenario: Several members wrong
- **WHEN** the body is `{"url": 1, "max_posts": "x", "max_comments": 1.5}`
- **THEN** the status is 422 and `details.fields` has `url`, `max_posts` and `max_comments`

#### Scenario: Integral value in decimal form
- **WHEN** the body has `"max_posts": 100.0`
- **THEN** the request is accepted with `max_posts` 100

### Requirement: Upstream timeout and body cap
Each scrape SHALL run under `context.WithTimeout` of `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`, and a deadline error SHALL become 504 `upstream_timeout` with `details.platform`, also when a module ignores its context. Every outbound `http.Client` SHALL have that timeout and SHALL cap response bodies at 16 MiB.

#### Scenario: Module ignores its context
- **WHEN** a module keeps running past the timeout without checking its context
- **THEN** the answer is 504 `upstream_timeout` at the deadline

#### Scenario: Oversized answer
- **WHEN** a platform answers with a body of more than 16 MiB
- **THEN** reading the body fails and the scrape does not buffer it

### Requirement: Panic recovery
A panic in a platform module SHALL be recovered and answered with 500 `internal_error` and a fixed message. The stack trace SHALL appear only in the redacted log, never in the response.

#### Scenario: Panicking module
- **WHEN** a module panics with a message containing the token
- **THEN** the status is 500, the body has no stack trace or token and the process keeps serving

### Requirement: Scraper logging and redaction
The service SHALL log JSON lines to stderr with `log/slog` through a redacting handler that replaces `Bearer <...>` values and the values of `SCRAPER_TOKEN`, `TWITTER_AUTH_TOKEN`, `TWITTER_CT0` and the path and contents of the file named by `INSTAGRAM_SESSION_FILE` with `[redacted]` in messages, attributes and panic traces. The access log line SHALL hold method, path, status, platform, url_kind and latency_ms and no request header.

#### Scenario: Secret in an error
- **WHEN** a module error text contains the value of `SCRAPER_TOKEN`
- **THEN** the emitted log line contains `[redacted]` and not the value

#### Scenario: Sentinel token
- **WHEN** requests with a sentinel `SCRAPER_TOKEN` succeed, fail, panic and are rejected
- **THEN** the sentinel appears in no log line, response body or response header

### Requirement: Plain-text normalization helper
`socialscraper.PlainText` SHALL use `golang.org/x/net/html` to remove tags, decode entities, turn `<br>` and `</p>` into `\n`, keep link text and trim the result.

#### Scenario: Shared spec example
- **WHEN** the input is `<p>Hello &amp; <a href="https://x.y">welcome</a></p><p>Bye</p>`
- **THEN** the output is `Hello & welcome\nBye`

### Requirement: Scraper fixtures
Recorded fixtures SHALL live in `internal/socialscraper/<platform>/testdata/` for `bluesky`, `mastodon`, `instagram`, `threads`, `facebook` and `x`, moved with `git mv` so their history is kept.

#### Scenario: Fixture location
- **WHEN** the repository is checked out
- **THEN** `scraper/tests/fixtures/` does not exist and each platform directory above has its fixtures in `testdata/`

### Requirement: Scraper image
`scraper.Dockerfile` at the repository root SHALL build `./cmd/scraper` with `golang:1.26-alpine` and `CGO_ENABLED=0` and run it in `debian:bookworm-slim` with `chromium`, `ca-certificates` and `fonts-liberation` installed from apt with `--no-install-recommends`, as a non-root user with `HOME=/tmp`, exposing port 8003 with a `HEALTHCHECK` that runs `scraper -healthcheck`. The image SHALL contain no secret and no interpreter of the earlier implementation's language.

#### Scenario: Container serves health
- **WHEN** the image is built and started with `SCRAPER_TOKEN` set
- **THEN** `GET http://<container>:8003/health` answers 200

#### Scenario: Interpreter absent
- **WHEN** the image's `/usr/bin`, `/usr/local/bin` and `/usr/lib` are searched for an interpreter of the earlier implementation
- **THEN** none is found

### Requirement: Orchestrator image unchanged
The orchestrator `Dockerfile` and its build SHALL stay unchanged, and `.dockerignore` SHALL NOT list `scraper` while still excluding `.git`, `artifacts` and `openspec`.

#### Scenario: Orchestrator build
- **WHEN** the orchestrator image is built from `Dockerfile`
- **THEN** the build output and `ENTRYPOINT` are the same as before this change

## MODIFIED Requirements

### Requirement: Scraper configuration and secrets
The service SHALL read `SCRAPER_TOKEN` (required), `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` (default 30, a positive number, decimals allowed), `SCRAPER_LISTEN_ADDR` and platform credentials from the environment only. A missing token or an invalid timeout SHALL make the process exit non-zero with a message that names the variable and never its value. No setting value SHALL be logged; redaction is specified by "Scraper logging and redaction".

#### Scenario: Token missing
- **WHEN** the service starts without `SCRAPER_TOKEN`
- **THEN** the process exits non-zero with a message that names `SCRAPER_TOKEN` and serves nothing

#### Scenario: Invalid timeout
- **WHEN** `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` is `abc`, `0` or `-3`
- **THEN** the process exits non-zero with a message that names the variable and does not contain the value

#### Scenario: Decimal timeout
- **WHEN** `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` is `2.5`
- **THEN** each scrape is bounded by 2.5 seconds

#### Scenario: Authorization header in a log line
- **WHEN** a log message contains `Authorization: Bearer abc.def`
- **THEN** the emitted line contains `[redacted]` and not `abc.def`

#### Scenario: Timeout bound
- **WHEN** a platform module takes longer than `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`
- **THEN** the service answers 504 `upstream_timeout` with `details.platform` set

### Requirement: Platform module interface and registry
Platform support SHALL be the Go interface `Module` with `Scrape(ctx, Target, Request) (*Result, error)`; `Target` holds `Platform`, `URLKind`, `URL *url.URL` and `IDs`. The registry SHALL be a map from platform to `Module` filled by `Register(platform, module)` calls from files in `cmd/scraper`, so adding a platform needs no edit of core files. URL recognition for all six platforms SHALL live in `internal/socialscraper/urls.go`. Typed failures SHALL be `*socialscraper.Error` values.

#### Scenario: Unrecognized URL
- **WHEN** no platform recognizes the URL
- **THEN** the status is 422 with `error.code` `unsupported_url`

#### Scenario: Unexpected module failure
- **WHEN** a module returns an error that is not a typed scrape error
- **THEN** the status is 500 `internal_error`, the body has no stack trace or credential and the error text appears only in the redacted log

#### Scenario: Registration from a platform file
- **WHEN** a file in `cmd/scraper` calls `Register` for a platform
- **THEN** URLs of that platform reach its module and `platforms.go` is not edited

### Requirement: Platforms without a module
URL recognition SHALL cover all six platforms (`bluesky`, `mastodon`, `instagram`, `threads`, `facebook`, `twitter`). A recognized platform without a registered module SHALL answer 503 `platform_not_configured` with `details.platform`, never 500. Facebook `profile.php` URLs SHALL answer 422 `unsupported_account_type` with `details.account_type` `personal_profile` whether or not a Facebook module is registered.

#### Scenario: Module not installed
- **WHEN** a client scrapes `https://www.instagram.com/alice` and no Instagram module is registered
- **THEN** the status is 503 with `error.code` `platform_not_configured` and `details.platform` `instagram`

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
The Mastodon module SHALL contact only the host named in the scraped URL: it SHALL NOT recognize IP-literal or dotless hosts, SHALL NOT follow redirects and SHALL follow `Link: rel=next` only to the same scheme (`https`), host and port. A non-JSON answer SHALL mean the host does not serve the Mastodon API (422 `unsupported_url`).

#### Scenario: Link to another host
- **WHEN** `rel="next"` points at a different host
- **THEN** that URL is not requested

#### Scenario: Link with another scheme or port
- **WHEN** `rel="next"` points at the same host over `http` or on port 8443
- **THEN** that URL is not requested

#### Scenario: IP literal
- **WHEN** a client scrapes `https://127.0.0.1/@alice`
- **THEN** the status is 422 `unsupported_url` and no request is made

#### Scenario: Redirect
- **WHEN** the instance answers a request with a redirect
- **THEN** the redirect is not followed and the status is 422 `unsupported_url`

#### Scenario: Not a Mastodon host
- **WHEN** `/api/v1/accounts/lookup` answers 404 with an HTML body
- **THEN** the status is 422 `unsupported_url`

### Requirement: Offline scraper tests
Scraper tests SHALL run offline: package `internal/socialscraper/scrapertest` SHALL provide an `http.RoundTripper` that routes requests to per-host fixture handlers and fails the test on any unrouted host. Every platform module SHALL accept an injected `*http.Client`, and no test SHALL use `http.DefaultTransport` against a real host. Tests SHALL cover URL recognition table-driven, authentication, validation, error mapping, warnings, redaction and each module's success path.

#### Scenario: No network
- **WHEN** `go test ./... -race -count=1` runs without network access
- **THEN** all tests pass

#### Scenario: Unrouted host
- **WHEN** a test makes a request to a host that has no route
- **THEN** the test fails

### Requirement: Scraper CI job
The backend `repo-ci` workflow SHALL NOT contain a job for the earlier implementation and SHALL contain a `scraper-image` job for pull requests and `main` that builds `scraper.Dockerfile`, runs the container with `SCRAPER_TOKEN=ci-dummy`, checks that `/health` answers 200 and that `POST /scrape` without a token answers 401 with `WWW-Authenticate: Bearer`, and checks that no interpreter of the earlier implementation is in the image. Go tests SHALL run in the existing `repo-ci` job.

#### Scenario: Pull request touching the scraper
- **WHEN** a pull request changes files under `cmd/scraper` or `internal/socialscraper`
- **THEN** `repo-ci` runs the Go tests and `scraper-image` runs the image build and smoke checks

### Requirement: Scraper README
`README.md` SHALL have a "Scraper service" section: `go run ./cmd/scraper`, `docker build -f scraper.Dockerfile`, `docker run` on port 8003 with network `ml_net` and the operator's Traefik labels, placeholder values only. It SHALL document the scraper's environment variables (including the `INSTAGRAM_SESSION_FILE` migration and the removal of `SCRAPER_X_POOL_DB`) and the orchestrator's `SCRAPER_URL`, `SCRAPER_TOKEN`, `SCRAPER_TIMEOUT`.

#### Scenario: Operator instructions
- **WHEN** an operator reads the "Scraper service" section
- **THEN** it names `SCRAPER_TOKEN` and `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`, port 8003, network `ml_net` and Traefik labels, with placeholder values only
