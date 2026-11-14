# Tasks

## 1. Runtime
- [x] 1.1 `finalizer.New` / `Runtime.Run` implementing `engine.Finalizer`
- [x] 1.2 Response normalization and validation (built-in checks, then output schema)
- [x] 1.3 Insufficient-evidence short-circuit
- [x] 1.4 Error logging with job id and code only
- [x] 1.5 Registry default timeout 60s for the finalizer

## 2. Docs
- [x] 2.1 `docs/examples/final_synthesis.yaml`

## 3. Verification
- [x] 3.1 httptest-stub tests: success, 5xx, timeout, invalid_output cases, short-circuit, cancellation, finalize=false, slow analyzer, 50 concurrent settles x 100
- [x] 3.2 `go test ./... -race -count=1` and `scripts/validate-openspec.sh`
- [ ] 3.3 (after backend-api lands) register `finalizer.New(...)` in `cmd/orchestrator/main.go`
