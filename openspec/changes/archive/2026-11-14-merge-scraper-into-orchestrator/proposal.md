# Proposal: Merge the scraper into the orchestrator; remove Facebook and Chromium

## Why

Parent feature junction-2026/project#14, child issue junction-2026/backend#50. The scraper runs as a second binary
(`cmd/scraper`) and image (`scraper.Dockerfile`, Debian with a browser for the Facebook module) next to the
orchestrator. Only one image is supported, and Facebook, the only module that needs a browser, is not supported
for now.

## What Changes

- The scraper core (`internal/socialscraper`) runs inside the orchestrator: `cmd/orchestrator` builds the platform
  registry (bluesky, mastodon, instagram, threads, twitter) at startup over one shared `*http.Client`, and
  `internal/scraper` becomes an in-process adapter (no HTTP hop, no bearer token) with the existing input and error
  mapping.
- Scraping is on by default; `SCRAPER_ENABLED=false` disables it. `SCRAPER_URL`, `SCRAPER_TOKEN` and
  `SCRAPER_LISTEN_ADDR` are ignored with a startup warning. The X and Instagram credential variables move to the
  orchestrator unchanged and are redacted from its logs.
- **Removed**: `cmd/scraper`, `scraper.Dockerfile`, `internal/socialscraper/facebook` (with fixtures), the HTTP
  server, bearer authentication, body decoding and health check of `internal/socialscraper`, the browser
  dependencies in `go.mod`/`go.sum`, the `scraper-image` CI job. Facebook URLs fail with `unsupported_url` without
  any network request; `UnsupportedAccountType` is removed.
- README: the "Scraper service" section becomes "Scraping" with the new variables and a migration note.

## Shared specs

Pinned `junction-2026/shared-specs@6b5186fe71808717b0184f1ec28a4b3586a8d4d6`, change
`merge-scraper-drop-facebook` (capabilities `social-scraper-api`, `analysis-jobs-api`). Scraped results, scrape
error codes, the 503 `scrape_unavailable` answer and Facebook as `unsupported_url` are implemented as defined
there; the requirements of this change are backend-local (runtime, configuration, removal, tests, docs).

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `scraper-service`: in-process runtime and timeouts; standalone runtime, health check, authentication, body
  decoding, image and CI job removed; configuration, platforms, fixtures, tests and README updated.
- `scraper-facebook-x`: all Facebook requirements removed; X requirements without HTTP statuses; X registered by
  `cmd/orchestrator`.
- `scraper-meta-platforms`: registration in `cmd/orchestrator`.
- `orchestrator-http-server`: `SCRAPER_ENABLED`, ignored-variable warnings, single-process scrape tests.
- `analysis-engine`: the scraper client is an in-process adapter; error mapping and engine tests follow.
- `analyzer-registry`: reviewed, unchanged (it only says the README documents the scraper variables).

## Impact

- Deployments: one container. Remove the scraper container and its Traefik route; move the credential variables
  to the orchestrator.
- Code: `cmd/orchestrator`, `internal/scraper`, `internal/engine`, `internal/socialscraper`, `go.mod`, `go.sum`,
  `.github/workflows/ci.yml`, `README.md`.
