# Tasks

## 1. Registry
- [x] 1.1 `execution.spans` block, defaults, validation and `${VAR}` expansion (`internal/registry/technique.go`)
- [x] 1.2 `spans.timeout` shorter than `execution.timeout`; `loadLLM` error prefix for `spans.llm`

## 2. Adapter
- [x] 2.1 Span step with its own deadline and failure isolation (`internal/adapter/technique_spans.go`)
- [x] 2.2 Sentence method: split `Job.input.content`, one batch to the sentence model, thresholds
- [x] 2.3 Phrase method: span LLM request and schema, exact-substring acceptance, caps
- [x] 2.4 Result members `detected[].spans`, `span_method`, `span_status`, `spans_truncated`; redaction of span secrets

## 3. Docs and tests
- [x] 3.1 `config/technique_spans/prompt.md`, Dockerfile copy, example and README section
- [x] 3.2 httptest-stub tests for both methods, failures, deadlines, credentials and registry validation
