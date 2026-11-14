# Design

- **Hook, not a second scheduler.** `finalizer.Runtime` implements `engine.Finalizer`.
  The engine fires the barrier once under the per-job lock (pending -> running with
  `analysis.finalizing`) and runs the hook under `context.WithTimeout(jobCtx, timeout)`,
  so exactly-once, timeout and cancellation are the engine's; a second trigger is a
  no-op there. The runtime is stateless and only invoked once per job.
- **Registration.** `finalizer.New` returns a true nil interface when the registry has
  no enabled finalizer, so the engine reports `skipped` / `not_configured`.
- **Call.** Inputs come from `adapter.Inputs` (the example bindings give `content`,
  `results`, `coverage`); the call goes through the shared `engine.Executor`, so any
  adapter works and the same header and no-redirect rules apply.
- **Validation order.** Built-in FinalAssessment checks (verdict, categories,
  explanation, overall_score, category scores, insufficient_evidence + score, then
  evidence/limitations being arrays), then the configured `output.schema` against the
  service's own answer (before normalization). The message names the first failing member.
- **Normalization.** The response is decoded to `map[string]json.RawMessage`, defaults
  are filled and `produced_by` is set, so unknown members and number text stay verbatim.
- **Short circuit.** `Coverage.Succeeded == 0` builds the orchestrator assessment inside
  `Run`; the engine emits `analysis.finalizing` / `analysis.finalized` as usual.
- **Logging.** Only `job_id` and the error code are logged; messages never carry URLs or
  response bodies (the adapter already guarantees that for its errors).
