## Why

Scrape jobs only work for Bluesky and Mastodon; news articles and every other link fail with `unsupported_url`
although `source_info` already derives outlet, country and leaning from `input.source.url`. Backend child
junction-2026/backend#89 of junction-2026/project#26 adds a generic `web` scrape module that fetches any other
http(s) page through Oxylabs, extracts its main text and metadata and feeds the normal analysis.

Shared contract: shared-specs `fa8280bcd348502c3e84d6b4a6ebdcd913f03c75`, change `web-page-scraping`
(`social-scraper-api`, `analysis-jobs-api`). This change holds only backend-internal requirements.

## What Changes

- New package `internal/socialscraper/web`: Oxylabs Web API (default) and Web Scraper API request builders,
  go-trafilatura main-text extraction, metadata extraction, Markdown fallback, login-wall detector, in-memory
  cache.
- `internal/socialscraper`: `Recognize` applies the safety checks and falls back to `web`/`page`; `Scraper.Scrape`
  re-runs a Mastodon target as a web page when the host does not serve the Mastodon API; `Result` gains the page
  members (flattened) and `username: null` for pages; `LoginRequired` and `NoTextContent` errors; `WebConfig`.
- `internal/scraper`: `mapResult` maps `url_kind: page` (content, canonical/final URL source, title, author,
  published_at) and rejects a page without string `text`.
- `cmd/orchestrator`: registers the web module; the Oxylabs credentials join the log redactor.
- `internal/engine`: default scrape timeout 120 s → 360 s.
- README, `go.mod`/`go.sum` (`github.com/markusmobius/go-trafilatura`, Apache-2.0).
- Modified local requirements of `scraper-service` that said only Bluesky and Mastodon exist.

## Capabilities

### New Capabilities

- `web-page-scraper`: backend-local behaviour of the web page module (configuration, provider request builders,
  extraction, metadata, cache, timeouts, redaction, tests, README).

### Modified Capabilities

- `scraper-service`: routing, module registry, timeouts, configuration, redaction, fixtures and README now
  include the `web` platform.

## Impact

Files: `internal/socialscraper/{urls,config,model,errors,scraper,logging}.go`, `internal/socialscraper/web/**`,
`internal/scraper/scraper.go`, `internal/engine/engine.go`, `cmd/orchestrator/scraper.go`, `README.md`, tests,
`go.mod`, `go.sum`. No frontend change, no new endpoint. Oxylabs is never called by tests.
