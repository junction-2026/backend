# Proposal: create analysis jobs from a social-media URL (orchestrator)

## Why
junction-2026/project#8 lets clients analyze a social-media post or profile by URL. The shared change
`social-media-scrape-input` (shared-specs@7885bffdf154823a03961f0fb8dc0349d770752e) defines the `scrape`
request member, the `scraping` status, the Scrape object, the ScrapeResult→Input mapping, failure,
cancellation and scrape events (analysis-jobs-api, analysis-job-events) and the scraper service API
(social-scraper-api). This backend-local change implements them in the Go orchestrator
(junction-2026/backend#23).

## What changes
- Configuration `SCRAPER_URL`, `SCRAPER_TOKEN`, `SCRAPER_TIMEOUT` (orchestrator-http-server).
- New package `internal/scraper`: HTTP client for `POST /scrape`, tolerant ScrapeResult decoding, error mapping
  and the mapping to Input (analysis-engine).
- Scrape phase in the job goroutine before analyzer scheduling; `not_applicable` evaluated after input mapping
  for scrape jobs (analysis-engine).
- `docs/examples/bot_detection.yaml` (disabled by default) and README documentation (analyzer-registry).
- Tests: HTTP validation and 503, engine scrape phase against an `httptest` stub scraper.

Shared contracts are not restated here; the shared change is canonical for every REST and event member.
Not changed: `docs/examples/propaganda_detection.yaml`, `internal/registry` and the requirements of
`backend-mistral-translation`.
