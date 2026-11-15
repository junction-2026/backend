## 1. Implementation
- [x] 1.1 `internal/httpapi/public.go`: routes, validation order, composition, metadata, engine Create path
- [x] 1.2 PublicAnalysis mapping: result, techniques, bot_signal, evidence, error, share_url, coverage, input
- [x] 1.3 Global submit and get token buckets with injected clock, checked before the body is read
- [x] 1.4 `PUBLIC_API_SUBMIT_PER_MINUTE`, `PUBLIC_API_GET_PER_MINUTE`, `PUBLIC_BASE_URL` in `cmd/orchestrator`
- [x] 1.5 `docs/public-api.md`

## 2. Tests
- [x] 2.1 Every public-analysis-api scenario against `NewHandler` with a fake engine
- [x] 2.2 Validation precedence, composition with blank and whitespace comments, 100-comment limit
- [x] 2.3 Rate limits with an injected clock; `/api/v1/jobs` unaffected
- [x] 2.4 Mapping: classifier max score across `models`, llm confidence, bot_signal validity, evidence null/empty,
  wikipedia found-only
- [x] 2.5 Config defaults and errors naming the variable; end-to-end through `run` with the real engine

## 3. Validation
- [x] 3.1 `go test ./... -race`, `go vet ./...`, `scripts/validate-openspec.sh`
