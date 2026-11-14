# Proposal: backend-remove-closed-platform-scrapers

Parent: junction-2026/project#24, child junction-2026/backend#78. Shared contract: shared-specs
`6d1e0c05eeee99ea3099ca5c00e6e8a7c8e16dad`, change `remove-closed-platform-scraping` (capabilities
`social-scraper-api`, `analysis-jobs-api`).

## Why
The closed platforms cannot be scraped reliably without breaking their terms of service or paying for APIs.
Only Bluesky and Mastodon, which have open public APIs, stay supported.

## What Changes
- Delete the three closed-platform module packages under `internal/socialscraper` with their fixtures, their
  platform constants and URL recognition, and the `login_required` helper in `errors.go`.
- `listedHosts` in `urls.go` keeps only `bsky.app`: every other host is a Mastodon candidate when its path has a
  Mastodon shape; any other URL fails with `unsupported_url` without a request.
- `cmd/orchestrator` registers only the Bluesky and Mastodon modules; no platform credential variable is read.
- The redactor drops the removed credential variables and no longer reads a session file
  (`NewRedactor(getenv, extra...)`); `Bearer` values and `SCRAPER_TOKEN` stay redacted.
- `platform_not_configured` stays for a recognized platform without a registered module.
- Tests switch to Bluesky/Mastodon fixtures and URLs with the same assertions; new tests cover the unsupported
  URLs and the former platform host probed as Mastodon.
- README "Scraping" section lists only Bluesky and Mastodon.

## Impact
Local capabilities: `scraper-service` (modified, added, removed requirements), `scraper-meta-platforms` and
`scraper-facebook-x` (every requirement removed), `analysis-engine` and `orchestrator-http-server` (scenarios).
No API shape change; Bluesky and Mastodon modules unchanged.
