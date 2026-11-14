## 1. Implementation
- [x] 1.1 `adapter.JobSourceLanguage` and the `$job.source_language` binding in `adapter.Inputs`
- [x] 1.2 Registry accepts `$job.source_language` only on `after_all_analyzers_settled`
- [x] 1.3 `LLMUserMessage` adds `<source_language>`; finalizer passes the language
- [x] 1.4 `config/finalizer/prompt.md` and `docs/examples/final_synthesis.yaml`

## 2. Tests
- [x] 2.1 Adapter: binding resolves to "ru" / nil; user message with and without the element
- [x] 2.2 Registry: rejected on other triggers, accepted on after_all
- [x] 2.3 Finalizer (httptest stub): ru, `RU ` -> ru, en, failed/skipped/absent/null, injection; system message byte for byte; unbound http finalizer unchanged

## 3. Validation
- [x] 3.1 `go test ./... -race -count=1`, `scripts/validate-openspec.sh`
