## Why

The `llm` adapter fails a call with `invalid_output` when the model's reply is cut off (`finish_reason: length`), is not JSON or fails the schema; for the finalizer the job then has no verdict (junction-2026/project#25, child junction-2026/backend#83). The shared change `llm-reply-retry` (shared-specs @ ee96c66b2ae1ddc6d3ccd8580fff23865568454b) adds `attempts`, `retry_reason` and summed `usage` to FinalAssessment and TopicExtraction. This local change specifies the backend runtime: one reply-level retry, its registry settings, prompt text, time budget and logging.

## What Changes

- `analyzer-registry`: `llm` executions accept `retry_invalid_output`, `retry_max_tokens`, `retry_model` and `retry_min_remaining`.
- `analyzer-adapters`: the `llm` adapter sends at most one reply-level retry per call for a cut-off, non-JSON or schema-failing reply, independent of the existing 429/5xx/connection retry; usage of both attempts is combined; retries are logged without content. "LLM reply decoding" now distinguishes retryable from non-retryable invalid replies. TopicExtraction results carry `attempts` and `retry_reason`.
- `analysis-finalizer`: the stored FinalAssessment carries `attempts` and `retry_reason` set by the runtime (1 or 2 for `llm`, null otherwise); output members with those names are dropped.
- Technique-span calls use the same adapter (with the default retry settings); their results expose no new members.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `analyzer-registry`: LLM execution settings, new retry settings validation.
- `analyzer-adapters`: LLM reply decoding, topic extraction normalization, new reply retry requirements.
- `analysis-finalizer`: Response normalization, new retry members requirement.

## Impact

`internal/adapter/llm.go`, `internal/adapter/topic.go`, `internal/adapter/technique_spans.go` (call site), `internal/finalizer/finalizer.go`, `internal/job/job.go` (FinalAssessment members), `internal/registry/registry.go`, their tests and `README.md` config docs. No new dependency, endpoint or error code.
