# Proposal: Text-level bot_detection adapter, backend (junction-2026/project#73, junction-2026/backend#152)

## Why
Shared change `bot-detection-text-result` (shared-specs @ 92a5491bf6c142f2322523c4a24e41a5b04d4946, capability
`bot-detection-result`) defines the result of the analyzer `bot_detection`: a TextBotDetection `{label, score, scope: "text"}`
computed on the job's original text. This is the backend side: the adapter, its registry validation, the example and docs.

## What Changes
- New adapter `bot_detection` (`registry.AdapterBotDetection`, `internal/adapter/bot_detection.go`): one POST of
  `{"text": input.normalized_content}` to `execution.endpoint`, tolerant reading of the detector's answer, label mapping
  to `label`/`score`/`scope: text`, validation, redacted errors.
- Registry: the adapter value, a required endpoint, POST-only, no handler/poll_interval, bindings only `content: $input`.
- `docs/examples/bot_detection.yaml` becomes the text-level analyzer; README section; comments in
  `config/risk_score/risk_score.yaml` (`single_text_score: score`, still commented out).

## Contracts
Shared: `bot-detection-text-result` / `bot-detection-result` at the pinned SHA. The detector's request/response protocol is
backend-local (between the backend and an external service). Nothing of the shared contract is duplicated here.
The risk-score formula, `propaganda_detection` and `techniques` are unchanged.

## Impact
`internal/adapter`, `internal/registry`, `docs/examples/bot_detection.yaml`, `config/risk_score/risk_score.yaml` (comment only), `README.md`.
