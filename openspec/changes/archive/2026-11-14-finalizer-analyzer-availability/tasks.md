## 1. Implementation

- [x] 1.1 Add `availability`/`note` to AnalyzerRun and `job.Availability` mapping function
- [x] 1.2 Set both on settle (engine `settleLocked`) and on creation-time skips
- [x] 1.3 Add `availability`/`note` to LLM finalizer entries
- [x] 1.4 Rewrite availability and `uncertain` rules in `config/finalizer/prompt.md`

## 2. Tests

- [x] 2.1 Table-driven mapping test incl. dependency chains (`internal/job/availability_test.go`)
- [x] 2.2 Finalizer entry scenarios and stub-LLM request/system message (`internal/finalizer`)
- [x] 2.3 REST and `analyzer.*` event values on the real engine (`internal/ws`)
