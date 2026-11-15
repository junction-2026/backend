## Why

junction-2026/project#79: a review of 140 analyses found prompt-level problems in the backend: `propaganda_detection` PROPAGANDA treated as a strong signal although the model is a coarse classifier, `bot_detection` output leaking into verdicts, jargon in `user_text`, posts-only wording for articles, `user_text` in German or Dutch for English input, `evidence_certainty_mismatch` firing on ordinary news, and generic or bundled fact-check claims. Child issue junction-2026/backend#156.

## What Changes

- `config/finalizer/prompt.md`: content-neutral wording, classifier weighting for `propaganda_detection`, a `bot_detection` rule, a news-report rule, plain-language `user_text` rules, output language from `<source_language>`.
- The finalizer request always carries `<source_language>CODE (English name)</source_language>` (`en` when the language is unknown).
- Finalizer-only soft language check of `user_text` (stopword detector over en, de, fr, es, it, nl, pt) that triggers the single reply retry with `retry_reason` `user_text must be written in <language>`.
- `config/llm_techniques/prompt.md` (narrower `evidence_certainty_mismatch`, two examples) and `execution.min_confidence` default 0.6.
- `config/extractor/prompt.md`: 3-8 word queries, one assertion per claim, group generalisations check-worthy.
- `propaganda_detection` `llm.description` (docs example) states the coarse-classifier reading.

Shared contract: `analysis-text-precision` @ 4fc0ca1d2b786df742a6c463619b5c5f15ddc66f. Non-goals: bot_detection config/adapter, risk score, models, pipeline, the country-association prompt (project#75).

## Impact

Code: `internal/adapter/{llm.go,translation_english.go}`, `internal/finalizer/finalizer.go`, `internal/registry/llm_techniques.go`, the three prompt files, `docs/examples/{propaganda_detection,llm_techniques}.yaml`. `adapter.LLM` and `llmChat` keep their signature and behaviour for every other caller.
