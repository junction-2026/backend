# Tasks

## 1. Core
- [x] 1.1 `internal/socialscraper`: config, errors, model, registry, URL recognition for six platforms, plain text
- [x] 1.2 Server: health, bearer auth, validation, timeout, panic recovery, access log
- [x] 1.3 Redacting `slog` logger, outbound client with timeout and 16 MiB cap
- [x] 1.4 `cmd/scraper`: main, `-healthcheck`, graceful shutdown, `platforms.go`

## 2. Platforms
- [x] 2.1 Bluesky module (direct XRPC)
- [x] 2.2 Mastodon module (REST, Link pagination, no redirects)

## 3. Tests, fixtures
- [x] 3.1 `scrapertest` router; `git mv` of all fixtures to `internal/socialscraper/<platform>/testdata/`
- [x] 3.2 Tests: URLs, validation, auth, timeouts, panics, redaction, Bluesky, Mastodon, healthcheck

## 4. Image, CI, docs, removal
- [x] 4.1 `scraper.Dockerfile`, `.dockerignore`
- [x] 4.2 `scraper-image` CI job replaces the `scraper` job
- [x] 4.3 README "Scraper service" section
- [x] 4.4 Delete `scraper/`
