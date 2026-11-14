# Tasks

## 1. Runtime
- [x] 1.1 `socialscraper.Scraper` (recognize, lookup, run with panic recovery, error mapping); delete server, auth, body decoding, global registry
- [x] 1.2 `internal/scraper` in-process adapter with the existing input and error mapping
- [x] 1.3 `cmd/orchestrator`: registry over one shared client, redacting logger, `SCRAPER_ENABLED`, ignored-variable warnings

## 2. Removal
- [x] 2.1 Delete `cmd/scraper`, `scraper.Dockerfile`, `internal/socialscraper/facebook`
- [x] 2.2 Facebook hosts → `unsupported_url`; remove `UnsupportedAccountType`
- [x] 2.3 `go mod tidy` drops the browser dependencies
- [x] 2.4 Remove the `scraper-image` job from `.github/workflows/ci.yml`

## 3. Tests
- [x] 3.1 Config tests: `SCRAPER_ENABLED` default, false, invalid; ignored-variable warning without the value
- [x] 3.2 Adapter tests: pass-through, panic, internal error, invalid result, timeout, cancellation
- [x] 3.3 End-to-end handler test: Bluesky, Mastodon, Instagram, Threads, X and Facebook URLs through the single orchestrator with `scrapertest` fixtures
- [x] 3.4 URL recognition tests for Facebook URLs; module tests offline

## 4. Docs and specs
- [x] 4.1 README "Scraping" section, env table, migration note
- [x] 4.2 Local spec deltas; `scripts/validate-openspec.sh`
- [ ] 4.3 At archive: replace the Purpose of `scraper-facebook-x` and `scraper-service` as in design.md
