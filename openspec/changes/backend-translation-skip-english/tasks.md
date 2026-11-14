# Tasks

## 1. Registry
- [x] 1.1 `skip_english`, `english_min_words`, `english_min_ratio` with defaults, validation, rejected for other adapters
- [x] 1.2 Registry tests: defaults, invalid values, other adapters

## 2. Adapter
- [x] 2.1 `internal/adapter/translation_english.go` (word lists, `detectEnglish`)
- [x] 2.2 Skipped result in `mistralTranslation` before the HTTP request
- [x] 2.3 Table-driven check test and httptest adapter tests (English, German, unsure, skip_english false, truncated)
- [x] 2.4 Engine test: `techniques` and `propaganda_detection` bound to a skipped result

## 3. Docs and validation
- [x] 3.1 Comment in `docs/examples/propaganda_detection.yaml`
- [x] 3.2 `go test ./...`, `go vet ./...`, `scripts/validate-openspec.sh`
