# Tasks

## 1. Job model and content normalization
- [x] 1.1 `internal/job`: Job, Input, AnalyzerRun, Coverage, Finalization, FinalAssessment, RunError, Record
- [x] 1.2 `normalized_content` (NFC, line endings, trim, HTML text extraction)

## 2. Registry
- [x] 2.1 YAML loader for a file or directory, defaults, `${VAR}` expansion
- [x] 2.2 Validation rules, output schema compilation and validation, selection closure

## 3. Adapters
- [x] 3.1 `http_sync` and `http_async` per analyzer-service-protocol (headers, polling, DELETE on abort)
- [x] 3.2 `custom` adapter, handler registry, built-in `text_stats`
- [x] 3.3 Input binding resolution

## 4. Engine
- [x] 4.1 Job creation: selection, applicability, creation skips, finalization configuration
- [x] 4.2 DAG scheduler with concurrency limits, timeouts, failure and skip propagation
- [x] 4.3 Settle barrier, `Finalizer` hook, job completion
- [x] 4.4 Cancellation in every phase
- [x] 4.5 Event bus with seq, snapshot subscribe and slow-consumer handling
- [x] 4.6 Store interface, in-memory store, retention eviction

## 5. Verification
- [x] 5.1 Unit tests for every requirement (`go test ./... -race -count=1`)
- [x] 5.2 `scripts/validate-openspec.sh`
