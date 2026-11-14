# Tasks

## 1. Registry
- [x] 1.1 `llm` adapter settings, defaults, `${VAR}` expansion, response_format and extra_body checks
- [x] 1.2 Analyzer `llm` block validation; startup checks and prompt read for an enabled llm finalizer
- [x] 1.3 Registry tests (defaults, expansion, every rejection, key never in errors)

## 2. Adapter
- [x] 2.1 `internal/adapter/llm.go`: request, modes, decoding, retries, redacted errors
- [x] 2.2 Adapter unit tests (redaction before truncation, decoding, Retry-After cap, content escaping)

## 3. Finalizer
- [x] 3.1 New FinalAssessment normalization/validation; old shape handling and tests removed
- [x] 3.2 LLM entries, short-circuit on opted-in analyzers, no-input warning
- [x] 3.3 Stub `/chat/completions` tests for acceptance criteria 1-10 and the edge cases

## 4. Process, image, docs
- [x] 4.1 `cmd/orchestrator` registers the finalizer; startup and end-to-end tests with the docs examples
- [x] 4.2 `config/finalizer/prompt.md` (verbatim base prompt) and Dockerfile copy
- [x] 4.3 `docs/examples/final_synthesis.yaml`, `propaganda_detection.yaml` llm block, README

## 5. Verification
- [x] 5.1 `go test ./... -race -count=1`
- [x] 5.2 `scripts/validate-openspec.sh`
