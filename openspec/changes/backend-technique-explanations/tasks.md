## 1. Implementation

- [x] 1.1 Glossary artifact (21 entries x 8 languages, templates), embedded and validated at startup
- [x] 1.2 `techniques` result: `explanation_language`, `glossary_version`, `plain_language`, span `why`
- [x] 1.3 Registry adapter `llm_techniques` with settings, defaults and validation
- [x] 1.4 `llm_techniques` adapter: inputs, prompt message, reply validation, span acceptance, filtering, ordering, failures, redaction
- [x] 1.5 `config/llm_techniques/prompt.md`, Dockerfile, `docs/examples/llm_techniques.yaml`, README

## 2. Tests

- [x] 2.1 Stub-only Go tests for every scenario of this change and the shared delta
- [x] 2.2 `go test ./...` and `scripts/validate-openspec.sh` pass
