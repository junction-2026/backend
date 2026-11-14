# Tasks

- [x] 1.1 Job model: `scraping` status, `input_unavailable` reason, Scrape object, nullable input
- [x] 1.2 `internal/scraper` client: request, auth, tolerant decoding, error mapping, Input mapping
- [x] 1.3 Engine scrape phase: start, success, failure, cancellation, timeout, deferred `not_applicable`
- [x] 1.4 HTTP API: `scrape` validation, 503 `scrape_unavailable`, `scraping` list filter
- [x] 1.5 Config `SCRAPER_URL`, `SCRAPER_TOKEN`, `SCRAPER_TIMEOUT`
- [x] 1.6 `docs/examples/bot_detection.yaml` and README
- [x] 2.1 Tests: HTTP table tests, scraper client tests, engine tests with an `httptest` stub scraper, config tests
- [x] 2.2 `go test ./... -race -count=1` and `scripts/validate-openspec.sh`
