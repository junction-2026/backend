# Proposal: Facebook (chromedp) and X (web GraphQL) modules for the Go scraper

## Why

Parent feature junction-2026/project#12 rebuilds the social-media scraper in Go. The core (child issue
junction-2026/backend#33, change `backend-scraper-go-core`) provides the server, registry, redacting logs and the
Bluesky and Mastodon modules. This change (child issue junction-2026/backend#35) adds the two platforms that need
unofficial interfaces: Facebook public pages through headless chromium driven with chromedp, and X through its web
GraphQL endpoints with the `auth_token` and `ct0` cookies. It replaces the Playwright and twscrape wording of the
local `scraper-facebook-x` capability.

## What Changes

- New packages `internal/socialscraper/facebook` (chromedp behind an internal `Browser` interface) and
  `internal/socialscraper/x` (GraphQL client; query ids, feature flags and bearer in `x/endpoints.go`).
- `cmd/scraper/platforms_fbx.go` registers both modules; `cmd/scraper/main_test.go` expects them registered.
- `go.mod` / `go.sum`: adds `github.com/chromedp/chromedp`.
- X fixtures become hand-built GraphQL responses (`x/testdata/`); Facebook fixtures are used as they are.
- `SCRAPER_X_POOL_DB` is no longer read; there is no account-pool database.

## Shared specs

Pinned `junction-2026/shared-specs@606cc9356f539952b9c63f2004353aa3c388a452`, no shared change, capability
`social-scraper-api`. Endpoints, ScrapeResult, warnings and error codes are implemented as defined there; the
requirements of this change are backend-local (browser handling, upstream client, secrets, tests, image).

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `scraper-facebook-x`: chromedp Facebook module with a browser interface, the X GraphQL client replacing the account pool, offline Go tests, image wording.
