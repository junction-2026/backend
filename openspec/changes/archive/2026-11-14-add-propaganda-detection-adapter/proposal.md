# Proposal: add propaganda_detection adapter

## Why

Parent feature junction-2026/project#4: run the IDA-SERICS/PropagandaDetection service as an
analyzer. The service takes `{"text": ...}` and answers `{"result": [{"label", "score"}], "latency_ms"}`,
which does not fit the generic `http_sync` request body (`{"job_id", "analyzer_id", "inputs"}`).

## What Changes

- New registry adapter `propaganda_detection` (endpoint, headers, timeout; no handler, poll_interval or bindings; method POST only).
- New adapter in `internal/adapter/propaganda.go`: one POST of `{"text": normalized_content}`, response validated, returned verbatim.
- `docs/examples/propaganda_detection.yaml` and a README note. The sample registry in `config/analyzers` is unchanged (an enabled analyzer with an unset env var aborts startup).
- Tests: adapter (keyword mock service), registry, one engine-level job.

## Shared specs

No shared-specs change (pinned `junction-2026/shared-specs@8d3a6d7cfeef2ae2fc100d6bbd9b5adee795837b`): the
shared `analyzer-service-protocol` (`http_sync`/`http_async` body unchanged) and `analysis-jobs-api`
(`AnalyzerRun.result` is the service JSON object; codes `adapter_error`, `timeout`, `cancelled`, `invalid_output`) stay satisfied.

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `analyzer-registry`: execution settings and validation
- `analyzer-adapters`: new adapter requirements, messages rule extended

## Impact

`internal/registry/registry.go`, `internal/adapter/{adapter,propaganda}.go`, tests, `docs/examples/`, `README.md`.
