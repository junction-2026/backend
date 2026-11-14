# Tasks

## 1. Extractor prompt
- [x] 1.1 `config/extractor/prompt.md`: 2-4 word queries, leave-out list, plans/decisions are claims, new examples
- [x] 1.2 Prompt test with stub LLM (`internal/adapter/topic_prompt_test.go`)

## 2. fact_check
- [x] 2.1 `factcheck.go`: retry query, trigger, `retry_of`, ordering, accounting, caching
- [x] 2.2 Stub-server tests: query building, trigger, ordering, bound of 12, cache, 400 on a retry

## 3. Verification
- [x] 3.1 `go test -race ./...`, `scripts/validate-openspec.sh`
