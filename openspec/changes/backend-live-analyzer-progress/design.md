# Design

Based on backend @ 2f661630a36fc9d7130872162bf86e1456a15fbd and shared-specs @ 052e3685233b676ef61999d78748c170e360b73a.

- `progress.done` is a counter advanced by each settle event of a planned analyzer, not derived from run states: an
  analyzer skipped at creation (`not_applicable`) is already settled in the job when the plan is emitted but must
  still advance `done` with its own event. `running` is derived from run states. Every Progress value is replaced,
  never mutated, so published events and snapshot copies stay valid.
- Activity facts switch on the adapter as well as on the key, so an analyzer with a borrowed key but another adapter
  never reads inputs it does not have.
- Previews decode with `UseNumber` so numbers are copied verbatim. Non-string list elements are skipped when texts are
  collected; the `*_total` counters count all elements.
- `Engine.Cancel` of a queued job marks the run cancelled before `beginLocked`, so no plan is emitted after
  cancellation was accepted.
