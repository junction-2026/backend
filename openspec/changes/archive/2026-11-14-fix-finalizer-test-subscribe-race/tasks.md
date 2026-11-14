# Tasks

## 1. Test harness
- [x] 1.1 `gatedFinalizer` and gate job in `gate_test.go`; `newEngine` sets `MaxParallelJobs: 1`
- [x] 1.2 `create()` queues the gate job, subscribes, then releases the gate
- [x] 1.3 `run()` uses the id returned by `create()`
- [x] 1.4 Review every `create()`/`runsOf()`/`engine.New` call site
- [x] 1.5 `TestSlowAnalyzerDelaysOnlyFinalization` asserts the last `analyzer.succeeded` precedes `analysis.ready_for_finalization`

## 2. Verification
- [x] 2.1 `go test -race -count=200 -run TestSlowAnalyzerDelaysOnlyFinalization ./internal/finalizer/`
- [x] 2.2 `go test -race -count=20 ./internal/finalizer/` under GOMAXPROCS=1 and a CPU burner
- [x] 2.3 Delay-injection reproduction (see design.md)
