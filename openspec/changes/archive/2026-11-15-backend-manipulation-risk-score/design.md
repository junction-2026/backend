# Design

- `risk.Compute(*job.Job, Settings) Score` is pure: no I/O, no clock, no randomness. `job` does not import `risk`, so `Job.Risk` is a
  `json.RawMessage` (like `Finalization.Result`); the engine marshals the Score once and stores/emits the same bytes. The stores
  already marshal the whole Job, so the round trip is verbatim.
- `Settings` = `Config` (the RiskConfig the Score echoes) + `BotMapping`. The mapping is deployment-only and not echoed.
- Mapping paths are dot-separated member names of the `bot_detection` result; a numeric segment indexes an array.
- Weight sets must sum to 1 and give intensity and confirmation a positive weight, so scores stay within 0..1 and renormalisation
  never divides by zero. `dampening_factor` must lie in 0..1 for the same reason.
- Engine config: `Config.Risk` (zero value means the defaults); `cmd/orchestrator` loads it from `RISK_SCORE_CONFIG`.
- Interrupted/cancelled/scrape-failed jobs never reach `barrierLocked`, so their `risk` stays null without extra code.
