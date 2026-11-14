# Tasks

## 1. Implementation
- [x] 1.1 `internal/chat`: config, limiter, history window, grounding, persona, upstream client
- [x] 1.2 `internal/httpapi/chat.go`: route, validation, error mapping; shared body reader
- [x] 1.3 `cmd/orchestrator`: chat configuration, wiring, log redaction of the key
- [x] 1.4 README: configuration, container networking, key rotation

## 2. Tests
- [x] 2.1 chat package: window, grounding trimming and delimiters, limiter, language
- [x] 2.2 HTTP tests against an in-process fake OpenAI-compatible server
- [x] 2.3 configuration tests; start without a key

## 3. Validation
- [x] 3.1 `go vet ./...`, `go test ./...`, `scripts/validate-openspec.sh`
