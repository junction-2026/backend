## Why

The `techniques` analyzer names each detected technique in English only and does not say, in the language of the post, what the technique means or why a highlighted phrase is an instance of it; three weaknesses outside the 18 SemEval-2020 labels are not detected at all (junction-2026/project#27, child junction-2026/backend#86). The shared change `technique-explanations-and-llm-techniques` (shared-specs @ 73d6f8995f729ad027b1ccb6f79d35e43eed911d, capabilities `technique-classifier-result` and `llm-technique-result`) defines the result contract. This local change specifies the backend runtime: the glossary artifact, the template rendering, the `llm_techniques` adapter, its registry settings, prompt and example configuration.

## What Changes

- `analyzer-adapters`: a fixed, versioned technique glossary embedded in the binary (18 SemEval names and the 3 LLM labels, 8 languages, `why` templates); `techniques` results gain `explanation_language`, `glossary_version`, `plain_language` and span `why` rendered by literal template substitution; new `llm_techniques` adapter (prompt input, reply validation, exact-substring span acceptance, filtering, ordering, failures, credential redaction).
- `analyzer-registry`: adapter `llm_techniques` with its settings and defaults; `min_confidence` and `max_findings_per_label` rejected on other adapters; image prompt default and example registry file with a finalizer description that marks the labels as LLM-assessed and weaker.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `analyzer-adapters`: new glossary, plain-language and `llm_techniques` requirements.
- `analyzer-registry`: new `llm_techniques` settings, example and image requirements.

## Impact

`internal/adapter` (technique.go, technique_spans.go, new technique_glossary.go and technique_glossary.yaml, new llm_techniques.go, llm.go schema selection, adapter.go dispatch and inputs), `internal/registry` (new llm_techniques.go, registry.go wiring), `config/llm_techniques/prompt.md`, `Dockerfile`, `docs/examples/llm_techniques.yaml`, README configuration docs and tests. No new dependency, endpoint or error code.
