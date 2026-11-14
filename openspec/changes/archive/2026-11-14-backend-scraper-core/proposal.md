# Proposal: Scraper service core (FastAPI, auth, URL recognition, Bluesky, Mastodon)

## Why

Parent feature junction-2026/project#8 lets a job start from a social-media URL. The shared contract
`social-scraper-api` (shared change `social-media-scrape-input`) describes the HTTP service that turns a URL
into a profile, history and comments. This change builds that service in the backend repository under
`scraper/`: the FastAPI app, authentication, URL recognition for all six platforms, the error envelope, the
platform-module interface and registry, and the Bluesky and Mastodon modules. Instagram, Threads, Facebook and
X modules are separate follow-up tasks that plug into the registry.

## What Changes

- New Python 3.12 service `scraper/` (`app/`, `tests/`, `Dockerfile`, `pyproject.toml`, `README.md`).
- Platform modules implement one interface and are listed in one registry file.
- Platforms recognized but without a module answer `503 platform_not_configured`.
- `.github/workflows/ci.yml` gains a `scraper` job (pytest + image build); the Go jobs are unchanged.
- No Go code changes.

## Shared specs

Pinned `junction-2026/shared-specs@7885bffdf154823a03961f0fb8dc0349d770752e`, change
`social-media-scrape-input`, capability `social-scraper-api`. All HTTP behaviour (endpoints, auth, request
validation, URL scenarios, ScrapeResult, warnings, error codes) is defined there and implemented here without
restating or altering it. The requirements in this change are backend-local: runtime, configuration, module
structure, test setup and CI.

## Capabilities

### New Capabilities
- `scraper-service`: runtime, configuration, platform-module registry, Bluesky and Mastodon access, offline tests, CI and README of the scraper service.

### Modified Capabilities
- none
