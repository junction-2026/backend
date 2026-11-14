# Tasks

## 1. Localization runtime
- [x] 1.1 `internal/localization`: languages, JobLocalization types, source-language rules, fixed table
- [x] 1.2 Batched provider request, prompt, reply validation, timeout, redacted logging
- [x] 1.3 LRU cache per (job, language) of translated texts, no caching of `unavailable`, in-flight deduplication

## 2. HTTP and wiring
- [x] 2.1 Route `GET /api/v1/jobs/{id}/localization`, validation and error envelope, read-only job access
- [x] 2.2 `LOCALIZATION_TIMEOUT` and wiring in `cmd/orchestrator`; README lists the endpoint

## 3. Tests and verification
- [x] 3.1 Table tests for statuses, errors, source-language rules, fixed text, cache, deduplication, failures
- [x] 3.2 Job immutability and no events; one test against a job loaded from the SQLite store
- [x] 3.3 `go test ./...`, `go vet ./...`, `scripts/validate-openspec.sh`
