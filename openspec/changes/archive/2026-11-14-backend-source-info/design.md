# Design: `source_info` (backend)

- **Table in the registry.** The registry reads the outlet table while it normalizes an enabled `source_info` analyzer and keeps it in `Execution.Outlets`; the adapter only reads it. A per-`Load` map shares one table between analyzers with the same file. This makes a bad file a startup error that names the analyzer, with no extra wiring in `main` or the `Executor`.
- **Immutability.** `outlets.Table` has no mutators after `Load`; lookups only read maps, so concurrent jobs need no lock.
- **Duplicates.** A host or domain may repeat inside one outlet (the main domain is normally also in `domains` and `hosts`); only a value seen under an earlier outlet is an error.
- **Binding `source`.** `$input.source` is bound as `source` (optional, so `page_outlet` is `null` when it is not bound); the registry accepts only `search` and `source`.
- **Search analyzer.** `search` must name an analyzer whose adapter is `news_search`; checked after all analyzers are loaded.
- **Trigger.** Only `on_dependencies_succeeded`, so a failed or skipped `news_search` gives the canonical dependency skips and `no_sources` needs no new trigger.
- **IP hosts** are never matched by registered domain (no eTLD+1 of an address).
