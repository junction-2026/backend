# Proposal: Mistral translation analyzer feeding propaganda_detection

## Why

Parent feature junction-2026/project#7: the PropagandaDetection service expects English text, but jobs
carry any language. A `mistral_translation` analyzer translates `normalized_content` into English and
`propaganda_detection` takes its text from it.

## What Changes

- New registry adapter `mistral_translation` (endpoint, api_key, model, timeout, max_input_chars, headers).
- New adapter in `internal/adapter/translation.go`: one chat-completions POST, JSON-object reply validated.
- `propaganda_detection` accepts an optional `input.bindings.text` (`$input` or `$analyzers.<id>`); the
  blanket "bindings rejected" rule is replaced.
- `docs/examples/propaganda_detection.yaml` defines both analyzers; README documents them. `config/analyzers` is unchanged.
- Tests: adapter (fake Mistral and propaganda servers), registry, one engine-level job.

## Shared specs

No shared-specs change (pinned `junction-2026/shared-specs@729fe96720e155868fb944c773470ac4619779f0`):
the analyzer result is an `AnalyzerRun.result` object and the error codes `adapter_error`, `timeout` and
`cancelled` already exist. All requirements are backend-local.

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `analyzer-registry`: execution settings and validation, propaganda binding, example
- `analyzer-adapters`: Mistral translation requirements, propaganda detection request

## Impact

`internal/registry/registry.go`, `internal/adapter/{adapter,propaganda,translation}.go`, tests,
`docs/examples/propaganda_detection.yaml`, `README.md`.
