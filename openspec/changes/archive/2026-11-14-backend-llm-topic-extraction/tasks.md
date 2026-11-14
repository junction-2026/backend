# Tasks

## 1. Registry
- [x] 1.1 `execution.task`, `max_input_chars` for topic_extraction, defaults, trigger/task combinations, bindings rejected
- [x] 1.2 Startup checks and error text for topic_extraction; finalizer messages unchanged
- [x] 1.3 Registry tests

## 2. Adapter
- [x] 2.1 `topic.go`: schema, input cut, request, validation, normalization; dispatch in `adapter.go`; schema/name choice in `llm.go`
- [x] 2.2 Mock-LLM tests (success, empty, invalid outputs, retries, 401 redaction, timeout, cancellation, truncation, blank)
- [x] 2.3 Finalizer opt-in and invalid-extraction isolation tests

## 3. Docs and image
- [x] 3.1 `config/extractor/prompt.md` and the Dockerfile COPY line
- [x] 3.2 `docs/examples/topic_extraction.yaml`, README section

## 4. Verification
- [x] 4.1 `go test -race ./...`
- [x] 4.2 `scripts/validate-openspec.sh`
