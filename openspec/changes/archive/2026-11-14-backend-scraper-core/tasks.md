# Tasks

## 1. Core
- [x] 1.1 `scraper/` project: `pyproject.toml`, `app/` package, settings (`SCRAPER_TOKEN`, `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`)
- [x] 1.2 Error envelope, request/response models, plain-text and timestamp helpers
- [x] 1.3 Route handling: auth first, JSON/validation errors, timeout, 500 envelope, log redaction
- [x] 1.4 URL normalization, platform interface, registry, pending platforms (all six recognized)

## 2. Platforms
- [x] 2.1 Bluesky module (atproto, public AppView)
- [x] 2.2 Mastodon module (REST, Link pagination, HTML to text)

## 3. Tests, image, CI, docs
- [x] 3.1 Offline tests with JSON fixtures and respx
- [x] 3.2 `scraper/Dockerfile` (git in apt line, port 8003)
- [x] 3.3 `scraper` CI job (pytest, docker build)
- [x] 3.4 `scraper/README.md`
