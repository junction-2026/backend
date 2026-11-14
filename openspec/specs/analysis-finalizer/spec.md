# analysis-finalizer Specification

## Purpose
Backend-local runtime of the finalizer: registration with the engine's Finalizer hook, the call to the finalizer service, normalization and validation of its FinalAssessment, the insufficient-evidence short-circuit and error logging. It implements the shared analysis-jobs-api, analysis-job-events and analyzer-service-protocol finalization contracts.

## Requirements

### Requirement: Finalizer registration
The finalizer runtime SHALL register with the engine's Finalizer hook when the registry contains an enabled `after_all_analyzers_settled` analyzer; with a disabled or absent one no runtime is registered and finalization is `skipped` with reason `not_configured`.

#### Scenario: Enabled finalizer
- **WHEN** the registry has an enabled `final_synthesis` finalizer
- **THEN** jobs run the finalizer service after all analyzers settle

#### Scenario: Disabled or absent finalizer
- **WHEN** the finalizer is `enabled: false` or not defined
- **THEN** every job ends `completed` with `finalization` `skipped` / `not_configured` and no service call

### Requirement: Exactly-once finalization
Finalization SHALL be started by an atomic `pending` to `running` transition of the job's finalization state under the per-job lock; any second trigger SHALL be a no-op. Under concurrent analyzer completions the finalizer service SHALL be called once and `analysis.finalizing` and `analysis.finalized` SHALL each be emitted once.

#### Scenario: Fifty concurrent settles
- **WHEN** 50 analyzers settle at the same moment
- **THEN** the finalizer stub receives one request and each finalization event is emitted once

### Requirement: Finalizer call bounds
The finalizer call SHALL run under a context with the configured `execution.timeout` (60s when unset for a finalizer) derived from the job context, so job cancellation aborts it. Finalization SHALL start only after every AnalyzerRun settled, and failed or skipped analyzers SHALL NOT prevent it.

#### Scenario: Timeout
- **WHEN** the finalizer service does not answer within the timeout
- **THEN** finalization is `failed` with `error.code` `timeout`, AnalyzerRuns are unchanged and the job ends `completed`

#### Scenario: Cancel during finalization
- **WHEN** the job is cancelled while the service call is running
- **THEN** the call is aborted, `analysis.finalization_failed` (`cancelled`) is emitted, then `job.cancelled` and no `job.completed`

#### Scenario: Cancel before the barrier
- **WHEN** the job is cancelled while an analyzer is still running
- **THEN** finalization is `skipped` with reason `cancelled` and the service is not called

#### Scenario: Request disables finalization
- **WHEN** a job is created with `options.finalize` false
- **THEN** the service is not called and finalization is `skipped` with reason `disabled_by_request`

### Requirement: Finalizer request inputs
The finalizer request inputs SHALL be resolved from the configured bindings. For the example entry (`content: $input`, `results: $job.analyzers`, `coverage: $job.coverage`) they contain `content` (Input with `normalized_content`), `results` (every settled AnalyzerRun) and `coverage`.

#### Scenario: Example bindings
- **WHEN** the example finalizer runs for a job with two succeeded analyzers
- **THEN** the request has `inputs.content`, two `inputs.results` and `inputs.coverage`

### Requirement: Response normalization
The runtime SHALL store an absent or null `overall_score` as null and absent or null `evidence` and `limitations` as `[]`, set `produced_by` to `finalizer` and keep every other member verbatim as raw JSON; it SHALL NOT compute or adjust any score.

#### Scenario: Minimal response
- **WHEN** the service returns `{"verdict":"low_risk","categories":[],"explanation":"No signs found."}`
- **THEN** the stored result has `overall_score: null`, `evidence: []`, `limitations: []` and `produced_by: finalizer`

#### Scenario: Unknown members
- **WHEN** the service returns an extra member `"extra": {"a": [1.50]}`
- **THEN** the stored result contains it unchanged

### Requirement: Response validation order
The runtime SHALL apply the built-in FinalAssessment checks of analyzer-service-protocol first and the configured `output.schema` second; a failure SHALL fail finalization with `invalid_output` and a message naming the first failing member, with all AnalyzerRuns unchanged and the job ending `completed`. A present `evidence` or `limitations` that is not an array is also `invalid_output`.

#### Scenario: Missing members
- **WHEN** the service returns `{"verdict":"low_risk"}`
- **THEN** finalization is `failed` with `invalid_output` and the message names `categories`

#### Scenario: Built-in before schema
- **WHEN** the response has an unknown verdict and also violates the output schema
- **THEN** the error message names `verdict`

#### Scenario: Schema violation
- **WHEN** the response passes the built-in checks but violates `output.schema`
- **THEN** finalization is `failed` with `invalid_output`

### Requirement: Insufficient-evidence short-circuit
When no AnalyzerRun succeeded the runtime SHALL NOT call the service and SHALL return an assessment with verdict `insufficient_evidence`, `overall_score` null, explanation "No analyzer produced a usable result; no assessment score can be given.", `produced_by` `orchestrator`, one `missing_evidence` limitation and one `failed_analyzer` limitation (with `analyzer_ids`) per failed analyzer. `analysis.finalizing` and `analysis.finalized` SHALL be emitted as in a normal run.

#### Scenario: All analyzers failed
- **WHEN** two analyzers fail and none succeeds
- **THEN** finalization is `completed` with three limitations and the service received no request

### Requirement: Finalizer logging
Finalizer errors SHALL be logged with the job id and the error code only. Finalizer response bodies and the finalizer URL SHALL NOT be logged.

#### Scenario: Service error
- **WHEN** the service answers HTTP 500 with a body
- **THEN** the log line has `job_id` and `code=adapter_error` and contains neither the body nor the URL
