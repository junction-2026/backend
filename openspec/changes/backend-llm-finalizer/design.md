# Design: LLM finalizer (backend-llm-finalizer)

## Context

Backend @ 554a945: `internal/finalizer` validated the old FinalAssessment; `cmd/orchestrator/main.go`
never set `engine.Config.Finalizer`, so every job ended `skipped / not_configured` even with a
finalizer in the registry. Shared change `llm-finalizer-verdict` @ 729fe96 defines the new result.

## Decisions

- **Settings live on `registry.Execution`.** The llm fields are extra members of the execution
  block; `${VAR}` expansion, the emptiness checks and the prompt read happen in `registry.Load`
  for an enabled llm finalizer, so `main` fails at startup with the registry's usual
  `file: analyzer "id": message` error. Errors name the field or variable, never a value.
- **The prompt is read once at load** into `Execution.Prompt`; the system message is that string.
- **The llm call is not an `engine.Executor` analyzer.** It needs other analyzers' `llm` blocks and
  returns usage next to the output, so the finalizer runtime builds the entries and calls
  `adapter.LLM(ctx, def, userMessage)` directly. `Executor.Execute` rejects `llm` (the registry
  already restricts it to the finalizer).
- **Entries** are compact JSON objects, one per opted-in AnalyzerRun in job order (registry id
  order): `{id, description, status, result}` when succeeded, otherwise `{id, description, status,
  reason}` where reason is `error.code` (failed) or the skip reason. `encoding/json` escapes `<`
  and `>` inside entries, so a result cannot close `<analyzers>`; `</content>` in the content
  (any case) is written as `<\/content>`.
- **Validation** is the shared rule set (verdict enum, non-empty `user_text`, `confidence` null or
  0..1) plus a configured `output.schema`, in every mode. The strict JSON schema sent to the
  provider requires `confidence` (OpenAI strict mode needs every property required); validation
  still accepts it absent. Extra members are dropped; `usage` comes only from the provider reply.
- **Retries**: one retry after 1s (429: `Retry-After` seconds, capped at 5s) for 429, 5xx and
  connection errors before a response, inside the finalizer context. Other statuses fail at once.
- **Redaction**: the provider's `error.message` has every occurrence of the API key replaced by
  `[REDACTED]` before truncation to 300 characters, so no key prefix survives the cut. Adapter
  messages never contain the URL. The runtime logs job id and error code only.

## Risks

- OpenAI strict mode may not honour `minLength`; validation enforces non-empty `user_text` anyway.
- A model may ignore the schema in `json_object`/`none` mode; the result is `invalid_output`.
- Only the literal API key is redacted; a provider echoing a transformed key is not caught.
