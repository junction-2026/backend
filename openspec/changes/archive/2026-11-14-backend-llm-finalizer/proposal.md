# Proposal: LLM finalizer (backend-llm-finalizer)

## Why

Parent feature junction-2026/project#6 (child junction-2026/backend#14): the finalizer moves from an
external `http_sync` service at `${FINALIZER_URL}` to an LLM behind any OpenAI-compatible
`POST {base_url}/chat/completions` API. The LLM gives a three-way verdict, its confidence and a
plain-language text for the user, from the outputs of the analyzers that opt in to LLM input.

## What Changes

- **Shared contract** (pinned `junction-2026/shared-specs@729fe96720e155868fb944c773470ac4619779f0`,
  change `llm-finalizer-verdict`): FinalAssessment becomes `{verdict, confidence, user_text,
  produced_by, usage}`; the insufficient-evidence short-circuit, finalizer response validation and
  normalization follow it. This change implements it; it does not restate it.
- **Registry**: new execution adapter `llm` (base_url, api_key, model, prompt_file, temperature,
  max_tokens, timeout, response_format, headers, extra_body), only on the finalizer; optional
  analyzer block `llm: {include, description}`; startup checks for an enabled llm finalizer.
- **Adapter** `internal/adapter/llm.go`: request, `response_format` modes, reply decoding, one retry
  on 429/5xx/connection errors, redacted provider errors.
- **Finalizer runtime**: new normalization/validation for every adapter, prompt assembly from the
  opted-in AnalyzerRuns, short-circuit without opted-in success, warning when nothing is opted in.
  The old `overall_score`/`categories`/`evidence`/`limitations` handling is removed.
- **Process**: `cmd/orchestrator` registers the finalizer runtime with the engine (it was never
  wired before); the base prompt ships as `config/finalizer/prompt.md`, copied by the Dockerfile to
  `/etc/orchestrator/finalizer/prompt.md`.
- **Docs**: `docs/examples/final_synthesis.yaml` is the llm entry; `propaganda_detection.yaml`
  gains an `llm` block; README documents `FINALIZER_BASE_URL`, `FINALIZER_API_KEY`,
  `FINALIZER_MODEL` and the `response_format` modes.

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `analysis-finalizer`: registration, normalization, validation, short-circuit, LLM input and prompt
- `analyzer-registry`: `llm` adapter settings, analyzer `llm` block, startup checks, example
- `analyzer-adapters`: `llm` adapter request, reply decoding, retries, errors
- `orchestrator-http-server`: container image ships the prompt; listing hides LLM settings

## Impact

- No client reads the old FinalAssessment (see the shared change). An external `http_sync`
  finalizer must now return `{verdict, confidence?, user_text}`; none is deployed.
- Tests use `httptest` stubs only; CI needs no network access or API key.
