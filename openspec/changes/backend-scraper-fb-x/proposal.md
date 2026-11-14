# Proposal: Scraper Facebook pages and X platform modules

## Why

Parent feature junction-2026/project#8 lets a job start from a social-media URL. The scraper service built by
`backend-scraper-core` recognizes Facebook and X URLs but answers `platform_not_configured` for them because their
modules are not installed. This change adds the two modules so those URLs return a normalized ScrapeResult.

## What Changes

- `scraper/app/platforms/facebook.py`: public Facebook pages and page posts through headless chromium (Playwright), reading the page's own GraphQL responses.
- `scraper/app/platforms/x.py`: X profiles and posts through twscrape with a cookie account pool built from `TWITTER_AUTH_TOKEN` and `TWITTER_CT0`.
- URL recognition for both platforms moves from `platforms/pending.py` into the modules; `app/registry.py` gets one entry each.
- `scraper/Dockerfile` installs chromium for Playwright; `pyproject.toml` gains `twscrape` and `playwright`.
- Recorded fixtures and offline tests; the existing "module not installed" test no longer lists Facebook and X.

## Shared specs

Pinned `junction-2026/shared-specs@7885bffdf154823a03961f0fb8dc0349d770752e`, change `social-media-scrape-input`,
capability `social-scraper-api`. All HTTP behaviour (URL scenarios, ScrapeResult, warnings, error codes including
`unsupported_account_type` and `platform_not_configured`) is defined there and implemented here unchanged. The
requirements in this change are backend-local: browser runtime, account pool storage and tests.

## Capabilities

### New Capabilities
- `scraper-facebook-x`: Facebook browser runtime, X account pool and offline test rules of the two platform modules.

### Modified Capabilities
- none
