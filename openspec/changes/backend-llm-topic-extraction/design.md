# Design

- **Reuse.** `adapter.LLM` (request, retry, redaction, reply decoding) serves both tasks; it picks the schema and
  `response_format` name from `execution.task`. Only the user message, the input cut and the result validation are new
  (`topic.go`). `LLMUserMessage` and the finalizer are untouched.
- **Input.** The analyzer reads the job's `normalized_content` like `mistral_translation`, with the same `cutAtSpace`
  rule; `input.bindings` are rejected at load (it analyzes the job input only).
- **Validation by decoding.** The reply is decoded into pointer-typed structs, so wrong types, `null` and missing
  members all fail with `invalid_output`; unknown members are dropped by the decoder.
- **Defaults.** `max_tokens` 2000 (10 topics plus 10 news items do not fit the finalizer's 600), timeout 30s,
  `prompt_file` `/etc/orchestrator/extractor/prompt.md`.
- **Schema modes.** In `json_object` and `none` modes the schema is appended to the user message, as for the finalizer.
