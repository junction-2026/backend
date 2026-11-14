# Proposal: Instagram and Threads modules in Go

## Why

Parent feature junction-2026/project#12 rebuilds the scraper service in Go. The core, Bluesky and Mastodon landed
with `backend-scraper-go-core` (junction-2026/backend#33); this change (child issue junction-2026/backend#34)
ports the Instagram (earlier: instaloader) and Threads modules, so `instagram.com` and `threads.net`/`threads.com`
URLs no longer answer `503 platform_not_configured`.

## What Changes

- New package `internal/socialscraper/instagram`: Instagram's public web JSON endpoints over `net/http`, endpoint
  paths and headers in `endpoints.go`, optional JSON session file `INSTAGRAM_SESSION_FILE` sent as cookies.
- New package `internal/socialscraper/threads`: page HTML fetched with `net/http`, embedded JSON extracted with
  `golang.org/x/net/html`, defensive mapping.
- `cmd/scraper/platforms_meta.go` registers both modules.
- Instagram fixtures under `internal/socialscraper/instagram/testdata` are rewritten into the raw web-endpoint
  response shape (hand-built, no real cookies); the Threads HTML fixtures are used unchanged.
- `go.mod`, `README.md`, CI and the Dockerfile are not touched.

## Shared specs

Pinned `junction-2026/shared-specs@606cc9356f539952b9c63f2004353aa3c388a452`, no shared change, capability
`social-scraper-api`. Endpoints, URL grammar, ScrapeResult, warnings and error codes are defined there and
implemented unchanged. The requirements here are backend-local: how the modules obtain data, their credentials
and their test setup.

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `scraper-meta-platforms`: Instagram access through public web endpoints (renamed from instaloader), the JSON session cookie file, failure mapping without instaloader, Threads access in Go, fixture locations and registration.
