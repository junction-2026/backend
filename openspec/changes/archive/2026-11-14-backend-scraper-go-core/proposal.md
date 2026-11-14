# Proposal: Scraper service in Go

## Why

Parent feature junction-2026/project#12 rebuilds the social-media scraper in the Go backend and removes the
other-language service that served the shared `social-scraper-api` until now. This change (child issue
junction-2026/backend#33) builds the core of the new service as the Go binary `cmd/scraper`: HTTP server,
configuration, authentication, validation, URL recognition for all six platforms, the error envelope, the
platform-module interface and registry, redacting logs, the Bluesky and Mastodon modules, offline test
tooling, the container image, the CI job and the README. It also deletes the old `scraper/` directory and moves
all recorded fixtures next to their Go modules. The Instagram, Threads, Facebook and X modules are follow-up
tasks that register themselves from their own files in `cmd/scraper`.

## What Changes

- New Go packages `cmd/scraper` and `internal/socialscraper` (with `bluesky`, `mastodon`, `scrapertest`).
- Fixtures move with `git mv` from `scraper/tests/fixtures/<platform>/` to
  `internal/socialscraper/<platform>/testdata/` for all six platforms.
- `scraper/` is deleted; `scraper.Dockerfile` at the repository root replaces its image build.
- `.github/workflows/ci.yml`: the `scraper` job is replaced by `scraper-image`; `repo-ci` already runs the Go tests.
- `README.md` gains a "Scraper service" section.
- The orchestrator (`cmd/orchestrator`, `internal/scraper`) and its `Dockerfile` are not changed.

## Shared specs

Pinned `junction-2026/shared-specs@606cc9356f539952b9c63f2004353aa3c388a452`, no shared change, capability
`social-scraper-api`. All HTTP behaviour (endpoints, auth, request validation, URL scenarios, ScrapeResult,
warnings, error codes) is defined there and implemented here without restating or altering it. The requirements
in this change are backend-local: runtime, configuration, structure, logging, image, tests and CI.

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `scraper-service`: Go runtime, configuration, registry, logging, Bluesky and Mastodon access, offline tests, image, CI job and README replace the earlier implementation.
