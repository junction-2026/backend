## Purpose
Backend-local structure of the social-media scraper service that implements the shared social-scraper-api: its runtime and container, configuration and secret handling, platform-module registry, the Bluesky and Mastodon modules, offline tests, CI and documentation.

## ADDED Requirements

### Requirement: Scraper service runtime and image
The scraper service SHALL live in `scraper/` of the backend repository as a Python 3.12 FastAPI application started by uvicorn on `0.0.0.0:8003`, and SHALL be built by `scraper/Dockerfile` whose `apt-get install` line includes `git`. The image SHALL contain no secret.

#### Scenario: Container serves health
- **WHEN** the image is built from `scraper/Dockerfile` and started with `SCRAPER_TOKEN` set
- **THEN** `GET http://<container>:8003/health` answers 200

#### Scenario: Git in the image
- **WHEN** the image is built
- **THEN** `git --version` succeeds inside it

### Requirement: Scraper configuration and secrets
The service SHALL read `SCRAPER_TOKEN` (required), `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` (default 30, a positive number) and platform credentials from the environment only. Startup SHALL fail with a message naming the setting when `SCRAPER_TOKEN` is unset or the timeout is invalid. No setting value SHALL be logged, and the log SHALL redact `Authorization` header values and the values of secret-named environment variables, including in tracebacks.

#### Scenario: Token missing
- **WHEN** the service starts without `SCRAPER_TOKEN`
- **THEN** the process exits non-zero with a message that names `SCRAPER_TOKEN` and serves nothing

#### Scenario: Authorization header in a log line
- **WHEN** a log message contains `Authorization: Bearer abc.def`
- **THEN** the emitted line contains `[redacted]` and not `abc.def`

#### Scenario: Timeout bound
- **WHEN** a platform module takes longer than `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`
- **THEN** the service answers 504 `upstream_timeout` with `details.platform` set

### Requirement: Platform module interface and registry
Platform support SHALL be a module interface: `recognize(url) -> (platform, url_kind, ids)` (or no match) and `async scrape(request) -> ScrapeResult`. All modules SHALL be listed in one registry; adding a platform SHALL need no edit of core files other than that list.

#### Scenario: Unrecognized URL
- **WHEN** no registered module recognizes the URL
- **THEN** the status is 422 with `error.code` `unsupported_url`

#### Scenario: Unexpected module failure
- **WHEN** a module raises an exception that is not a typed scrape error
- **THEN** the status is 500 `internal_error`, the body has no stack trace or credential and the traceback appears only in the redacted log

### Requirement: Platforms without a module
URL recognition SHALL cover all six platforms (`bluesky`, `mastodon`, `instagram`, `threads`, `facebook`, `twitter`). A recognized platform whose module is not installed SHALL answer 503 `platform_not_configured` with `details.platform`, never 500.

#### Scenario: Module not installed
- **WHEN** a client scrapes `https://www.instagram.com/alice` and no Instagram module is installed
- **THEN** the status is 503 with `error.code` `platform_not_configured` and `details.platform` `instagram`

### Requirement: Bluesky module
The Bluesky module SHALL use the atproto SDK against the public AppView `https://public.api.bsky.app` without authentication. It SHALL fill the profile members from the actor profile and `posts` from the author feed (own posts, reposts skipped, newest first, following the cursor until `max_posts`).

#### Scenario: Profile with history
- **WHEN** a client scrapes `https://bsky.app/profile/alice.bsky.social` with `max_posts` 5 and the author feed has a repost among 6 posts over two pages
- **THEN** the response has 5 own posts, newest first, none of them the repost

#### Scenario: Profile not found
- **WHEN** the AppView answers 400 for the profile
- **THEN** the status is 404 `account_not_found` with `details.platform` `bluesky`

### Requirement: Bluesky post thread and partial results
For post URLs the Bluesky module SHALL fill `post` and `comments` from the post thread (replies flattened, oldest first, at most `max_comments`). A failed feed request SHALL become a `history_unavailable` or `history_truncated` warning, and a missing replies list a `comments_unavailable` warning.

#### Scenario: Thread comments
- **WHEN** a post URL's thread has nested replies and one deleted reply
- **THEN** `comments` has the remaining replies flattened and oldest first, each with `in_reply_to_id` naming its parent

#### Scenario: Feed failure
- **WHEN** the author feed request fails after the profile was fetched
- **THEN** the response is 200 with `posts: []` and a `history_unavailable` warning

### Requirement: Mastodon module
The Mastodon module SHALL use the REST API of the instance named in the URL over HTTPS: `/api/v1/accounts/lookup`, `/api/v1/statuses/{id}`, `/api/v1/statuses/{id}/context` and `/api/v1/accounts/{id}/statuses`. It SHALL follow `Link: rel=next` until `max_posts`, exclude boosts, convert `content` and `note` HTML to plain text, and report `verified` as null.

#### Scenario: Link pagination
- **WHEN** the first statuses page has a `Link` header with `rel="next"` and `max_posts` is 4
- **THEN** the module requests the next page, returns 4 posts and stops

#### Scenario: Post comments
- **WHEN** a status URL is scraped and its context has descendants
- **THEN** `comments` are the descendants oldest first, capped at `max_comments`, and no context request is made when `max_comments` is 0

### Requirement: Mastodon host safety
The Mastodon module SHALL contact only the host named in the scraped URL: it SHALL NOT recognize IP-literal or dotless hosts, SHALL NOT follow redirects and SHALL follow `Link: rel=next` only on that host. A non-JSON answer SHALL mean the host does not serve the Mastodon API (422 `unsupported_url`).

#### Scenario: Link to another host
- **WHEN** `rel="next"` points at a different host
- **THEN** that URL is not requested

#### Scenario: IP literal
- **WHEN** a client scrapes `https://127.0.0.1/@alice`
- **THEN** the status is 422 `unsupported_url` and no request is made

#### Scenario: Not a Mastodon host
- **WHEN** `/api/v1/accounts/lookup` answers 404 with an HTML body
- **THEN** the status is 422 `unsupported_url`

### Requirement: Offline scraper tests
Scraper tests SHALL run offline with recorded JSON fixtures under `scraper/tests/fixtures/<platform>/` and an HTTP mock; no test SHALL contact a real platform. Tests SHALL cover URL recognition table-driven, authentication, validation, error mapping, warnings and each module's success path.

#### Scenario: No network
- **WHEN** `pytest` runs in `scraper/` without network access
- **THEN** all tests pass

### Requirement: Scraper CI job
The backend `repo-ci` workflow SHALL contain a `scraper` job that runs `pytest` in `scraper/` and builds `scraper/Dockerfile`; Go jobs and steps SHALL be unchanged.

#### Scenario: Pull request touching the scraper
- **WHEN** a pull request changes files under `scraper/`
- **THEN** the `scraper` job runs the tests and the image build

### Requirement: Scraper README
`scraper/README.md` SHALL document the environment variables, local run, docker run on port 8003 with network `ml_net` and the Traefik labels the operator adds, and SHALL contain no real token.

#### Scenario: Operator instructions
- **WHEN** an operator reads `scraper/README.md`
- **THEN** it names `SCRAPER_TOKEN` and `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`, port 8003, network `ml_net` and Traefik labels, with placeholder values only
