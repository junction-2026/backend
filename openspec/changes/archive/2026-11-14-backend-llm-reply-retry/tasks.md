## 1. Implementation

- [x] 1.1 Registry settings `retry_invalid_output`, `retry_max_tokens`, `retry_model`, `retry_min_remaining` with defaults and field-named validation errors
- [x] 1.2 Reply-level retry in `adapter.LLM` (length, invalid_json, schema), exclusions, time budget, retry model, combined usage, logging
- [x] 1.3 `attempts`/`retry_reason` on FinalAssessment (null for orchestrator and http finalizers) and TopicExtraction
- [x] 1.4 README config docs

## 2. Tests

- [x] 2.1 Stub-only Go tests for every scenario of the shared `llm-reply-retry` delta and this change
- [x] 2.2 `go test ./...` and `scripts/validate-openspec.sh` pass
