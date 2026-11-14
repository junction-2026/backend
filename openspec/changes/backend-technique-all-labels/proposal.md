# Proposal: backend-technique-all-labels

Child issue junction-2026/backend#69 of junction-2026/project#16. Implements the shared change `technique-all-labels`
(shared-specs @ f687c62bb7f4fc934e58a3f68da9a7324f934c93, capability `technique-classifier-result`).

## Why

`technique_classifier` picks one of two models (`short`/`long`) and reports only techniques above per-technique
thresholds plus per-sentence detections on the translation. #16 asks for every label's score from the NCI document
model (`nci-technique`) and/or the NCI short-post model (`nci-social`), each with its model and cutoff, both kept when
both run, unfamiliar labels kept.

## What changes

- Registry: `technique` and `social` blocks (`endpoint`, `headers`, `cutoff`), `social_max_words` (80) and
  `technique_min_words` (40) with `technique_min_words <= social_max_words + 1`. The old keys `short`, `long`,
  `threshold`, `thresholds`, `short_max_words` and `max_sentences` are rejected at load naming their replacement.
  The `spans` block is unchanged.
- Adapter: routing by word count to one or both models, one `POST {"text": [translated_text]}` per selected model
  (concurrent), `results` (ModelResult / TechniqueScore for all 18 labels plus unfamiliar ones), `detected` from
  `above_cutoff` with `models` and without `score`, fed to the unchanged span step. Removed members: `model`,
  `threshold`, `thresholds`, `techniques`, `max_score`, `latency_ms`, `sentences`, `offsets_in`,
  `sentences_truncated`, Detection `score`. All-or-nothing when both models run.
- Example YAML (new keys, `NCI_TECHNIQUE_URL` / `NCI_SOCIAL_URL`, new `llm.description`) and README.

## Impact

- `internal/registry/technique.go`, `registry.go`; `internal/adapter/technique.go`, `technique_spans.go` (Detection
  type and parsed score lists), `adapter.go` (comment); tests of both packages.
- `docs/examples/technique_classifier.yaml`, `README.md`.
- Deployments must rename their technique config keys; startup names each replacement.
- `propaganda_detection` unchanged.
