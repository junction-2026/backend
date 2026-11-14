# Technique detection strength and not_detected-by-default finalizer prompt

Parent: junction-2026/project#31, child junction-2026/backend#108. Shared contract: shared-specs change
`technique-detection-strength` @ 15b58705d5f6ae93e86134b79e4ae9a1f01ddc9a (`technique-classifier-result`,
`analysis-jobs-api`).

## Why
Borderline, span-less technique detections looked like strong ones to the LLM finalizer, and its prompt told it to
prefer `uncertain` when evidence is weak. A harmless announcement got `uncertain`. The backend must mark each
Detection's strength and tell the finalizer that weak hints alone mean `not_detected` with lower confidence.

## What Changes
- Registry: optional `execution.strong_margin` on `technique_classifier` (number 0..1, default 0.15); docs example
  and README document it.
- Adapter: after the span step each Detection gets `margin`, `located`, `strength`; the result gets
  `summary: {strong, weak}` (shared contract).
- Docs example `llm.description` explains strength and that weak detections are hints at most.
- `config/finalizer/prompt.md`: strong-signal list, three verdict rules, confidence guidance, consistency rule;
  replaces the local Uncertain rule requirement and the "Prefer uncertain" sentence.
- Stub-only tests.

## Impact
Backend only. Stored results without the new members are served unchanged (raw JSON).
