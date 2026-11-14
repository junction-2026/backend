# Proposal: `source_info` analyzer (backend)

## Why

Parent feature junction-2026/project#18, child issue junction-2026/backend#59. Implements the shared change
`source-info-result` (shared-specs @ b9031cf5ef9bf55661e1fe8e95464dae6b98cc1c, capability `source-info-result`): an analyzer
that looks up the outlet, political leaning and country behind each `news_search` article and behind the job's
`input.source.url` in the static table `data/outlets.csv`. The result shape, the `no_sources` skip reason and the
matching rule are the shared contract and are not repeated here; everything below is backend-local (table
loading and validation, registry keys, adapter, image, example).

## What Changes

- New package `internal/outlets`: CSV loader with header, empty-domain and duplicate host/domain validation (row named), host and domain lookup maps, host normalization and exact-host then eTLD+1 matching.
- Registry: adapter `source_info`, `execution.outlets_file` (default `/etc/orchestrator/data/outlets.csv`), bindings and dependency validation, table loaded once per file at startup for enabled analyzers.
- Adapter `source_info`: SourceInfo result, `no_sources` run-time skip, `adapter_error`/`cancelled` failures; no network requests.
- `Dockerfile` copies `data/outlets.csv`; `docs/examples/source_info.yaml`; README section.

Not changed: shared specs, `internal/job`, the engine, the `technique_classifier` adapter, registry code, example and requirements
(feature project#16 rewrites those in parallel). This change only ADDS requirements, named `Source info ...` and `Outlet table ...`.

## Impact

backend only. A new optional analyzer; no quota or network cost. The image grows by the size of `data/outlets.csv`.
