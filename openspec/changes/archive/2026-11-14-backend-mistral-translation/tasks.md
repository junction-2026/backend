# Tasks

## 1. Registry
- [x] 1.1 `mistral_translation` settings, defaults, `${VAR}` expansion of api_key and model, rejections
- [x] 1.2 `propaganda_detection` accepts only `input.bindings.text` (`$input` or `$analyzers.<id>`)
- [x] 1.3 Registry tests (rejections, loading, docs example)

## 2. Adapter
- [x] 2.1 `internal/adapter/translation.go` and dispatch in `Execute`
- [x] 2.2 `propaganda_detection` reads `text` from the bound translation result
- [x] 2.3 Adapter tests with httptest fakes (request, truncation, result, failures, key leaks)
- [x] 2.4 Engine-level test: translation -> propaganda body; failure -> `dependency_failed`

## 3. Docs
- [x] 3.1 `docs/examples/propaganda_detection.yaml` with `translation`
- [x] 3.2 README: variables, cost/latency, truncation, dependency

## 4. Verification
- [x] 4.1 `go test -race ./...`
- [x] 4.2 `scripts/validate-openspec.sh`
