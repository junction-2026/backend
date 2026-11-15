## Context

Shared change `analysis-text-precision` (@ 4fc0ca1d) fixes the contract; this change is the backend half plus local-only prompt rules.

## Decisions

1. **Finalizer-only language check.** `adapter.LLM` keeps its signature; a new `LLMWith` takes an optional soft-check callback (`func(json.RawMessage) bool` reporting a wrong-language reply) and `LLM` calls it with nil. Parallel work (#75) calling `adapter.LLM` is unaffected.
2. **Soft retry.** A valid first reply in the wrong language triggers the single retry. The retry's valid reply is kept whatever its language; an invalid or failed retry keeps the first reply (`attempts: 2`, language `retry_reason`). A first reply already retried for `length`, `invalid_json` or `schema` is never retried again. Retry disabled or too little time: the first reply stands (`attempts: 1`).
3. **Detector.** A new `detectLanguage` scores the word sets of en, de, fr, es, it, nl, pt and returns a language only when it passes the English check's thresholds (`>= minWords` words, ratio >= 0.25 and twice the best other language). `detectEnglish` stays as is.
4. **Language names** come from a backend table of the seven detector languages plus common others; unknown codes fall back to `en (English)`.
5. **Prompt wording** is covered by string tests on the embedded file, not by model calls.
