## 1. Code

- [x] 1.1 Delete the closed-platform module packages and fixtures under `internal/socialscraper`
- [x] 1.2 Shrink platform constants, URL recognition and `listedHosts` to Bluesky and Mastodon (`bsky.app` only)
- [x] 1.3 Delete the `login_required` helper from `errors.go`
- [x] 1.4 Drop removed credential variables and session-file reading from the redactor
- [x] 1.5 Register only Bluesky and Mastodon in `cmd/orchestrator`

## 2. Tests and docs

- [x] 2.1 URL tests for every unsupported URL of the acceptance criteria and the former platform host as Mastodon candidate
- [x] 2.2 Mastodon module test: HTML 404 and redirect from that host give `unsupported_url`
- [x] 2.3 Switch engine, finalizer, scraper, core and orchestrator tests to Bluesky/Mastodon fixtures and URLs
- [x] 2.4 README "Scraping" section
- [x] 2.5 Delta specs; `go build ./...`, `go vet ./...`, `go test ./...`, `go mod tidy`, `scripts/validate-openspec.sh`
