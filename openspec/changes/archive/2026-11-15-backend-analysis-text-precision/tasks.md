## 1. Prompts and config
- [x] 1.1 Finalizer prompt (rules 1, 3, 4, 5 of #79)
- [x] 1.2 `propaganda_detection` `llm.description` in docs/examples
- [x] 1.3 llm_techniques prompt, default `min_confidence` 0.6
- [x] 1.4 Extractor prompt

## 2. Runtime
- [x] 2.1 `<source_language>CODE (Name)</source_language>` always in the finalizer request
- [x] 2.2 Detector extended to classify among en, de, fr, es, it, nl, pt
- [x] 2.3 Finalizer-only soft language check and retry

## 3. Tests
- [x] 3.1 Prompt string tests, request shape, language retry scenarios, min_confidence, bot_detection pass-through
- [x] 3.2 Diff check: bot_detection.yaml, bot_detection.go, config/risk_score/ unchanged

## 4. Validation
- [x] 4.1 `go test ./...`, `scripts/validate-openspec.sh`
