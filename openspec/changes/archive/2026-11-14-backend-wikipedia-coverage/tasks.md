# Tasks

## 1. Coverage label
- [x] 1.1 `wikipedia_coverage.go`: labels, sentence splitting, standalone numbers, downgrade re-check
- [x] 1.2 `wikiLookup.Coverage`/`EvidenceSentence`; one call in `personalize`
- [x] 1.3 Table-driven tests: each label, downgrade, grouped/decimal numbers, today-event claim, cache-hit parity

## 2. Wikipedia quotes
- [x] 2.1 `job.FinalAssessment.WikipediaQuotes` (never null), `job.WikipediaQuote`
- [x] 2.2 Finalizer verification and debug logging of dropped quotes; orchestrator assessment `[]`
- [x] 2.3 `FinalizationSchema.wikipedia_quotes` (required)
- [x] 2.4 Tests: kept/paraphrase/no_coverage/malformed, orchestrator `[]`, finalizer input unchanged, SQLite round trip

## 3. Prompt and docs
- [x] 3.1 `config/finalizer/prompt.md` labels, no verdict, verbatim quotes only via `wikipedia_quotes`
- [x] 3.2 `docs/examples/wikipedia.yaml` `llm.description`
