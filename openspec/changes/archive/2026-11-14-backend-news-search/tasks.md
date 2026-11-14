# Tasks

## 1. Registry
- [x] 1.1 `news_search` settings, defaults, `${VAR}` expansion, validation, bindings, depends_on
- [x] 1.2 Registry tests

## 2. Engine
- [x] 2.1 Adapter-reported skip hook in `internal/engine/run.go` and engine test

## 3. Adapter
- [x] 3.1 Query building, language resolution, Mistral query translation
- [x] 3.2 Serper search with fallback, concurrency, retry, abort, cache
- [x] 3.3 Merging, result object, redaction
- [x] 3.4 Adapter tests with stub Serper and Mistral servers

## 4. Docs and verification
- [x] 4.1 `docs/examples/news_search.yaml`, README section
- [x] 4.2 `go test -race ./...`, `scripts/validate-openspec.sh`
