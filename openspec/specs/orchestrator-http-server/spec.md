# orchestrator-http-server Specification

## Purpose
Backend-local runtime behaviour of the orchestrator HTTP server: configuration and startup, health endpoint, request body and cursor handling, graceful shutdown, WebSocket delivery limits, logging rules, the container image and the test obligations. The endpoints themselves are defined by the shared capabilities.

## Requirements

### Requirement: Server configuration and startup
The server SHALL listen on the address in `LISTEN_ADDR` (default `:8080`) and SHALL load the analyzer registry from `ANALYZERS_CONFIG` (default `/etc/orchestrator/analyzers`). A configuration error (unreadable or invalid registry, invalid engine setting, unusable listen address) SHALL make the process print a clear message naming the problem to standard error and exit with a non-zero status without serving requests.

#### Scenario: Defaults
- **WHEN** neither variable is set
- **THEN** the server listens on `:8080` and loads `/etc/orchestrator/analyzers`

#### Scenario: Invalid registry
- **WHEN** `ANALYZERS_CONFIG` names a directory without analyzer files
- **THEN** the process exits non-zero and the message names the path

#### Scenario: Job completes without finalizer
- **WHEN** the server runs with the built-in `text_stats` analyzer and no finalizer registered
- **THEN** a created job ends `completed` with `text_stats` `succeeded` and `finalization.status` `skipped`

### Requirement: Health endpoint
`GET /healthz` SHALL respond 200 with `{"status":"ok"}` once the registry is loaded. It is an operational endpoint outside `/api/v1` and not part of the shared contract.

#### Scenario: Healthy
- **WHEN** a client calls `GET /healthz` on a running server
- **THEN** the status is 200 and the body is `{"status":"ok"}`

### Requirement: Request body handling
Request bodies SHALL be limited to 1 MiB with `http.MaxBytesReader`. JSON decoding SHALL ignore unknown members and SHALL distinguish syntax errors (400 `invalid_request`) from type and validation errors (422 `validation_failed` with `details.fields`).

#### Scenario: Oversized body
- **WHEN** a client posts a body larger than 1 048 576 bytes to `/api/v1/jobs`
- **THEN** the status is 413 with `error.code` `payload_too_large`

#### Scenario: Syntax versus type error
- **WHEN** a client posts `{"input":` and then `{"input": {"content": 5}}`
- **THEN** the first yields 400 `invalid_request` and the second 422 `validation_failed`

### Requirement: Pagination cursors
Pagination cursors SHALL be opaque base64url-encoded `(created_at, id)` pairs naming the last item of the previous page. A cursor that is not valid base64url or does not contain such a pair SHALL yield 400 `invalid_request`.

#### Scenario: Pages do not overlap
- **WHEN** a client lists 5 jobs with `limit=2` and follows `next_cursor` until it is `null`
- **THEN** it receives every job exactly once, newest first

#### Scenario: Malformed cursor
- **WHEN** a client calls `GET /api/v1/jobs?cursor=%21%21`
- **THEN** the status is 400 with `error.code` `invalid_request`

### Requirement: Graceful shutdown
On SIGTERM (or SIGINT) the server SHALL stop accepting requests, close every open WebSocket connection with code 1001, cancel running jobs and exit within 10 seconds.

#### Scenario: Shutdown with a subscriber
- **WHEN** the process receives SIGTERM while a client is subscribed to a running job
- **THEN** the client's connection closes with code 1001 and the process exits within 10 seconds

### Requirement: WebSocket delivery limits
Each WebSocket connection SHALL buffer at most 256 undelivered events. When the buffer overflows the server SHALL close the connection with code 4001 and reason `slow_consumer`. The server SHALL send a ping every 25 seconds and SHALL abandon a frame write that does not complete within 10 seconds.

#### Scenario: Client does not read
- **WHEN** a client subscribes and stops reading while more than 256 events are emitted
- **THEN** the connection ends with close code 4001 and reason `slow_consumer`

### Requirement: Logging
The server SHALL log with `log/slog` as JSON lines that carry `job_id` and `analyzer_id` where applicable. Logs MUST NOT contain request bodies, analyzer results, headers or analyzer endpoint URLs.

#### Scenario: Analyzer settled
- **WHEN** an analyzer fails with an error whose message contains its endpoint URL
- **THEN** the log line carries `job_id`, `analyzer_id` and the error code but not the message

### Requirement: Container image
The repository SHALL contain a multi-stage `Dockerfile` that builds a static binary into a non-root image exposing port 8080, with a sample analyzers directory at `/etc/orchestrator/analyzers` containing only the `text_stats` analyzer and the base finalizer prompt `config/finalizer/prompt.md` (the parent issue's base prompt verbatim) at `/etc/orchestrator/finalizer/prompt.md`.

#### Scenario: Image runs
- **WHEN** the image is started without configuration
- **THEN** the server listens on 8080 and `GET /api/v1/analyzers` lists only `text_stats`

#### Scenario: Prompt in the image
- **WHEN** an `llm` finalizer omits `prompt_file` in the image
- **THEN** the prompt is read from `/etc/orchestrator/finalizer/prompt.md`

### Requirement: Test obligations
Handler tests SHALL use `httptest` and a fake analyzer service and cover every endpoint and error case. WebSocket tests (late subscriber, finished job, ordering, slow consumer) SHALL use a real WebSocket client against `httptest.Server`. A test SHALL read the REST state after each event and compare it with the event payload for the same entity, and a test SHALL configure a secret-looking `FINALIZER_URL` and assert that `GET /api/v1/analyzers` does not contain it.

#### Scenario: Suite passes
- **WHEN** `go test ./... -race -count=1` runs
- **THEN** all tests pass

### Requirement: Analyzer listing hides LLM settings
`GET /api/v1/analyzers` items SHALL NOT contain `base_url`, `api_key`, `model`, `headers`, `extra_body`, `prompt_file` or the `llm` block of any analyzer (shared `analysis-jobs-api` "Registry listing hides execution settings").

#### Scenario: LLM finalizer listed
- **WHEN** the docs examples are loaded with a secret-looking API key
- **THEN** the analyzers response contains neither the key, the base URL, the model nor the `llm` description

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

### Requirement: Ignored scraper variables
`SCRAPER_URL`, `SCRAPER_TOKEN` and `SCRAPER_LISTEN_ADDR` of the former standalone scraper SHALL be ignored. When one of them is set, startup SHALL log one warning that names the variable and never its value, and SHALL continue.

#### Scenario: Old env file
- **WHEN** the orchestrator starts with `SCRAPER_URL=http://scraper:8003` and `SCRAPER_TOKEN=s3cret`
- **THEN** it starts, scrapes in process, logs one warning naming `SCRAPER_URL` and one naming `SCRAPER_TOKEN`, and no log line contains `http://scraper:8003` or `s3cret`

### Requirement: Scraper setting errors
A `SCRAPER_ENABLED` value other than `true` or `false` (in any case), a non-positive or unparsable `SCRAPER_TIMEOUT` or an invalid `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` SHALL abort startup with a message naming the variable but not its value.

#### Scenario: Invalid switch
- **WHEN** `SCRAPER_ENABLED` is `yes-secret`
- **THEN** the process exits non-zero with a message naming `SCRAPER_ENABLED` and not containing `yes-secret`

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

### Requirement: Job localization route
The server SHALL serve `GET /api/v1/jobs/{id}/localization` as defined by the shared `job-text-localization` capability (shared-specs @ 07b1fce6c316aa2ddf926ffab423dd721fb5b9b4). Other methods SHALL get 405 `method_not_allowed` with `Allow: GET`.

#### Scenario: Other method
- **WHEN** a client calls `POST /api/v1/jobs/j1/localization`
- **THEN** the status is 405 with `Allow: GET`

### Requirement: Job localization errors
The handler SHALL answer, in the api-conventions error envelope, a missing or blank `language` with 400 `invalid_request`, a `language` whose lower-case primary subtag is not supported with 400 `unsupported_language` and `error.details.supported` listing `en`, `fr`, `ar`, `de`, `fi` and `ru`, an unknown job with 404 `not_found` and a job that is not done with 409 `job_not_finished`.

#### Scenario: Unsupported language
- **WHEN** a client calls `GET /api/v1/jobs/j1/localization?language=xx`
- **THEN** the status is 400, `error.code` is `unsupported_language` and `error.details.supported` is `["en","fr","ar","de","fi","ru"]`

#### Scenario: Missing language
- **WHEN** a client calls `GET /api/v1/jobs/j1/localization` without `language`
- **THEN** the status is 400 with `error.code` `invalid_request`

#### Scenario: Region subtag
- **WHEN** a client calls it with `language=DE-at` for a done job
- **THEN** the status is 200 and `language` is `de`

#### Scenario: Russian accepted
- **WHEN** a client calls `GET /api/v1/jobs/j1/localization?language=ru-RU` for a done job
- **THEN** the status is 200 and `language` is `ru`

### Requirement: Localization reads the job only
The localization handler SHALL load the job through the existing job service (`Service.Get`, backed by the configured job store, SQLite with `JOB_STORE=sqlite`), so persisted and recovered done jobs are served like live ones. It SHALL NOT write to the job store, change the job or publish an event.

#### Scenario: Persisted job
- **WHEN** a done job is only in the SQLite store (not in the working set) and is localized
- **THEN** the status is 200 and the stored job and `GET /api/v1/jobs/{id}` are unchanged afterwards

#### Scenario: No events
- **WHEN** a subscriber listens on a job's event stream while the job is localized in three languages
- **THEN** it receives no event

### Requirement: Localization timeout setting
`LOCALIZATION_TIMEOUT` (Go duration, default `20s`) SHALL bound each translation provider call of the localization. A value that is not a positive duration SHALL be a configuration error naming the variable. On timeout the texts of that call SHALL be `unavailable`.

#### Scenario: Invalid timeout
- **WHEN** `LOCALIZATION_TIMEOUT=-1s`
- **THEN** the process exits non-zero and the message names `LOCALIZATION_TIMEOUT`

#### Scenario: Slow provider
- **WHEN** the provider does not answer within the timeout
- **THEN** the response is 200 and every text that needed a translation has `status: unavailable` and `text: null`

### Requirement: Localization cache
The server SHALL keep translated texts in memory per (job id, language), bounded to 1000 entries with least-recently-used eviction, and SHALL NOT persist them. It SHALL NOT store an `unavailable` text: a later request SHALL send only the texts still missing. Concurrent requests for the same (job, language) SHALL share one provider call.

#### Scenario: Repeated request
- **WHEN** the same job is localized into `ar` twice and the first answer was `translated`
- **THEN** both answers have the same texts and the provider received one request

#### Scenario: Failure is retried
- **WHEN** the first provider call fails and a second request is made
- **THEN** the second request calls the provider again

#### Scenario: Concurrent requests
- **WHEN** ten requests for the same job and language arrive while the provider call is in flight
- **THEN** the provider receives one request and all ten answers carry its translations

### Requirement: Localization logging
A failed provider call SHALL be logged with the job id, the language and a fixed reason only. Logs and response bodies SHALL NOT contain the provider URL, API key, configured header values, prompt, texts or provider messages.

#### Scenario: Provider error
- **WHEN** the provider answers 503 with a body naming its URL and key
- **THEN** the response is 200 with `unavailable` texts and neither the body nor the log contains the URL, the key or the provider message

### Requirement: Localization tests
Tests SHALL use a fake provider and cover every status and error path, the source-language rules, the orchestrator fixed text, cache hits and misses, no caching of `unavailable` results, deduplication, job immutability (deep equality of the stored job and byte equality of `GET /api/v1/jobs/{id}`) and the absence of events, and one test SHALL localize a job loaded from the SQLite store.

#### Scenario: Suite
- **WHEN** `go test ./...` runs
- **THEN** these tests pass without network access

### Requirement: Create body visibility flag
The create handler SHALL decode `public` as an optional strict JSON boolean (default true), SHALL reject `null` and any non-boolean with 422 `validation_failed` and `details.fields.public`, and SHALL validate it together with the other create-body checks for both `input` and `scrape` bodies, creating no job when any check fails.

#### Scenario: Default and explicit values
- **WHEN** a client posts a body without `public`, with `"public": true`, or with `"public": false`
- **THEN** the status is 201 and the Job has `public` true, true and false respectively

#### Scenario: Invalid values
- **WHEN** a client posts `"public": "no"`, `1` or `null` with an `input` or a `scrape` body
- **THEN** the status is 422 `validation_failed`, `error.details.fields` has the key `public` and no job is created

### Requirement: Propaganda map endpoints
The server SHALL serve `GET {basePath}/map/countries` and `GET {basePath}/map/countries/{country_code}/analyses` (shared change `propaganda-map-api`, shared-specs @ fa2a80df8674ccbbc6183931e95dbb7d7c8c5b85), registered in `httpapi.go` with their handlers in `internal/httpapi/map.go`. Other methods SHALL yield 405 `method_not_allowed` and errors SHALL use the api-conventions envelope. The handlers SHALL only read the store: no analyzer, scraper or network call and no job change.

#### Scenario: Aggregates
- **WHEN** two `RU` and one `DE` map analyses exist
- **THEN** `GET /api/v1/map/countries` is 200 with the `DE` then `RU` items and `analyses: 3`

#### Scenario: Wrong method
- **WHEN** a client sends `POST /api/v1/map/countries`
- **THEN** the status is 405 with `error.code` `method_not_allowed`

### Requirement: Map list parameters
The list handler SHALL accept `country_code` of exactly two ASCII letters (upper-cased), `limit` 1-100 (default 20) and an opaque cursor; any other value SHALL yield 400 `invalid_request`. A well-formed code without map analyses SHALL yield 200 `{"items": [], "next_cursor": null}`.

#### Scenario: Paging
- **WHEN** `RU` has 25 map analyses
- **THEN** the first page has 20 items and a cursor, the second 5 and `next_cursor: null`

#### Scenario: Malformed code
- **WHEN** a client requests `/api/v1/map/countries/DEU/analyses` or `/1A/analyses` or `?limit=0`
- **THEN** the status is 400 with `error.code` `invalid_request`

### Requirement: Map list items
Each item of the list SHALL carry `job_id`, `created_at`, `finished_at`, `status`, `country_code`, `country`, `outlet`, `source` (`url`, `title` or null), `techniques` (`strength` only when stored), `verdict` (null without final assessment) and a `snippet` (whitespace collapsed, at most 200 code points, U+2026 appended when cut).

#### Scenario: Snippet
- **WHEN** `input.content` is `"  Short\n\ntext "`
- **THEN** `snippet` is `"Short text"`
