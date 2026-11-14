# Tasks

## 1. Extraction
- [x] 1.1 `claims` in `TopicExtractionSchema`; validation and normalization in `topic.go`; `max_claims` accessor
- [x] 1.2 `config/extractor/prompt.md`
- [x] 1.3 Update existing topic tests for the required `claims`; claim tests

## 2. Engine
- [x] 2.1 `adapter.SkipError` and the settle in `run.exec`; engine test

## 3. fact_check
- [x] 3.1 Registry: adapter, keys, defaults, validation; tests
- [x] 3.2 `factcheck.go`: plan, search, retry, cache, rating table, assembly, redaction
- [x] 3.3 Stub-server tests (plan, merge, rating, cache, failures, redaction, skip)

## 4. Docs
- [x] 4.1 `docs/examples/fact_check.yaml`, `max_claims` in `topic_extraction.yaml`, README

## 5. Verification
- [x] 5.1 `go test -race ./...`
- [x] 5.2 `scripts/validate-openspec.sh`
