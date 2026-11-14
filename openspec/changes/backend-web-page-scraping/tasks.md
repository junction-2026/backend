## 1. Routing and core
- [x] 1.1 `Recognize`: safety checks, Bluesky, Mastodon, `web` fallback; Mastodon probe fallback in `Scraper.Scrape`
- [x] 1.2 `Result` page members, `username: null` for pages, `LoginRequired`/`NoTextContent`, `WebConfig` loading
## 2. Web module
- [x] 2.1 Web API and Web Scraper API request builders, retry budget, error mapping
- [x] 2.2 Extraction, metadata, Markdown fallback, login-wall detector, cache
## 3. Integration
- [x] 3.1 `mapResult` for `page`; module registration; redactor secrets; scrape timeout default
- [x] 3.2 README, go.mod/go.sum
## 4. Tests and validation
- [x] 4.1 Offline unit and integration tests with fixtures and a stub provider
- [x] 4.2 `go test ./...`, `scripts/validate-openspec.sh`
