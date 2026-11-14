# Design

## In-process scraper

`socialscraper.New(reg, log)` returns a `*Scraper` whose `Scrape(ctx, rawURL, Request) (*Result, *Error)` is what
the HTTP handler did, without HTTP: `Recognize`, registry lookup (no module: `platform_not_configured`), the module
call in its own goroutine (so the caller's deadline holds even when a module ignores its context) with panic
recovery (stack only in the redacted log, result `internal_error`), error mapping (typed errors pass through with
`details.platform`; deadline or network timeout: `upstream_timeout`; anything else: `internal_error`), then
`scraped_at`, `latency_ms` and `[]` for nil lists. The HTTP server, bearer check, body decoding and health check go
away with `cmd/scraper`; `POST /api/v1/jobs` already validates the ScrapeRequest.

`internal/scraper.Client{Core, Redactor}` keeps its `Scrape(ctx, job.ScrapeRequest)` signature, so the engine is
unchanged apart from messages. It marshals the core's `Result` to JSON and feeds it to the existing `mapResult`, so
`source.metadata.scrape` stays the ScrapeResult JSON and the validity check (platform, url_kind, post for post
URLs) is unchanged. A core error passes through with its code, redacted message and details, except
`internal_error`, which becomes `scraper_unavailable`. When the call fails after `ctx` ended, the result is
`timeout` (deadline) or `cancelled`, as before.

## Timeouts

`SCRAPER_TIMEOUT` (default 120s) is the engine's bound of one scrape and yields `timeout`.
`SCRAPER_UPSTREAM_TIMEOUT_SECONDS` (default 30) is now only the `http.Client` timeout of the shared client, so one
slow platform request yields `upstream_timeout`. The standalone service used it as a whole-scrape bound too; in
process that bound is `SCRAPER_TIMEOUT`.

## Startup

`cmd/orchestrator/scraper.go` registers the five modules on a fresh `Registry` with one `socialscraper.NewClient`
(upstream timeout, 16 MiB body cap). Tests replace the transport with a `scrapertest` router, so the end-to-end
handler test scrapes recorded fixtures without network. The global `Default` registry is removed. The orchestrator
logger becomes `socialscraper.NewLogger` with `NewRedactor`, so X cookies, the Instagram session file (path and
contents) and `Bearer` values never reach a log line. `SCRAPER_ENABLED=false` leaves `engine.Config.Scraper` nil,
which already answers 503 `scrape_unavailable`.

## Facebook

`facebook.com`, `fb.com` and `fb.watch` hosts are matched explicitly in `Recognize` and fall through to
`unsupported_url`; they never reach the Mastodon recognizer, so no request is made. The Facebook module, its
fixtures, the browser dependencies and `UnsupportedAccountType` are deleted.

## Spec housekeeping

The capability `scraper-facebook-x` keeps its name; its Purpose still names Facebook and the earlier account pool
and cannot be changed by a delta. The archiver SHALL replace it with: "Backend-local runtime of the X platform
module of the in-process scraper: the X web GraphQL client, credential handling, error mapping and offline tests."
Likewise the `scraper-service` Purpose should read "in-process scraper" instead of "scraper service".
