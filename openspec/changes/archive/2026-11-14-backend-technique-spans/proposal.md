# Proposal: backend-technique-spans

Child issue junction-2026/backend#65 of junction-2026/project#17. Implements the shared change `technique-spans`
(shared-specs @ 1f10ab06e5650d7567062a9add69a68cd24b07d2, capability `technique-classifier-result`).

## Why

`technique_classifier` scores the English translation; its `sentences[]` point into the user's text only for English
input. Users need the exact phrases of the text they submitted that carry each detected technique.

## What changes

- Registry: optional `execution.spans` block of `technique_classifier` (sentence model, span LLM, span deadline, caps).
- Adapter: after document scoring, a span step adds `detected[].spans`, `span_method`, `span_status` and
  `spans_truncated`. English input: sentences of `Job.input.content` scored in one batch by the `nci-social` sentence
  model. Other languages: the span LLM proposes phrases; only exact substrings of `Job.input.content` are kept.
- A span-step failure or its own deadline leaves the run `succeeded` with empty spans.
- Prompt file `config/technique_spans/prompt.md` (copied into the image), example, README.

## Impact

- `internal/registry/technique.go`, `registry.go` (`Spans` field, `loadLLM` error prefix, span timeout check).
- `internal/adapter/technique.go`, new `technique_spans.go`, `llm.go` (reply schema of the span LLM).
- `docs/examples/technique_classifier.yaml`, `README.md`, `Dockerfile`, `config/technique_spans/prompt.md`.
- No change to document scoring, `sentences` or `offsets_in`.
