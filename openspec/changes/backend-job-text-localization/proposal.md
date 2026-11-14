# Proposal: job text localization endpoint (backend)

## Why

Parent feature junction-2026/project#45, child issue junction-2026/backend#115. Implements the backend side of the
shared change `job-text-localization` (shared-specs @ 07b1fce6c316aa2ddf926ffab423dd721fb5b9b4, capability
`job-text-localization`): `GET /api/v1/jobs/{id}/localization?language=<code>` returns the finalizer `user_text` and
the `llm_techniques` finding `explanation`s of a done job in the UI language, each `original`, `translated` or
`unavailable`. The endpoint shape, status values, errors and source-language rules are the shared contract;
everything below is backend-local (route wiring, translation provider, prompt, timeout, cache, fixed-text table,
logging, tests).

## What Changes

- `internal/httpapi`: route `GET /api/v1/jobs/{id}/localization`, language validation, error mapping; the job is
  read through the existing `Service.Get` (job store, SQLite-backed with `JOB_STORE=sqlite`) and never written.
- New `internal/localization`: batched translation through the deployment's `mistral_translation` analyzer
  (endpoint, model, api_key, headers), reply validation, `LOCALIZATION_TIMEOUT` (default 20s), in-memory LRU cache
  per (job, language) with request deduplication, fixed translations of the orchestrator insufficient-evidence text.
- `cmd/orchestrator`: `LOCALIZATION_TIMEOUT` and the wiring. README lists the endpoint.

Not changed: shared specs, detection, thresholds, finalizer rules, confidence, evidence extraction, fact-checking,
retrieval, `internal/adapter/wikipedia*.go`, the `job-store` schema.

## Impact

backend only, additive. At most one provider request per (job, language) while the server runs (plus retries after
an `unavailable` answer). No new dependency, no schema change.
