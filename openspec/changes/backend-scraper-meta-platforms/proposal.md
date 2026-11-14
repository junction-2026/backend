# Proposal: Scraper Instagram and Threads modules

## Why

The scraper service core (`backend-scraper-core`) recognizes Instagram and Threads URLs but answers
`503 platform_not_configured` for them. Parent feature junction-2026/project#8 needs real profile, history and
comment data from both platforms.

## What Changes

- `scraper/app/platforms/instagram.py`: instaloader, anonymous, public profiles and posts, optional session file.
- `scraper/app/platforms/threads.py`: httpx and the page's embedded SSR JSON, defensive parsing.
- Their URL recognizers move out of `platforms/pending.py` into the modules; `app/registry.py` lists the two
  modules instead of the `Pending` entries. `instaloader` joins the dependencies in `scraper/pyproject.toml`.
- Recorded fixtures under `scraper/tests/fixtures/instagram|threads/`, tests that never open a socket.
- README section for both platforms and the `INSTAGRAM_SESSION_FILE` setting.

## Shared specs

Pinned `junction-2026/shared-specs@7885bffdf154823a03961f0fb8dc0349d770752e`, change
`social-media-scrape-input`, capability `social-scraper-api`. Endpoints, URL grammar, ScrapeResult, warnings and
error codes are defined there and implemented here unchanged. The requirements of this change are backend-local:
how the two modules obtain data, their credentials and their test setup.

## Capabilities

### New Capabilities
- `scraper-meta-platforms`: data access, credential handling, failure mapping and offline tests of the Instagram and Threads modules.

### Modified Capabilities
- none
