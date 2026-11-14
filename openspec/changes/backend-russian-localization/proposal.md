# Proposal: Russian (ru) job-text localization

## Why
Parent feature junction-2026/project#60 adds `ru` as a platform language. Shared change `russian-localization-language`
(shared-specs @ 293a1a1b5d86de5d8fd59bbe3938ba053d799b47) adds it to `job-text-localization`.

## What Changes
- `localization.Supported` becomes en, fr, ar, de, fi, ru.
- A Russian translation of `finalizer.InsufficientText` joins the fixed-text table.
- Tests cover `ru` in `Language()`, the HTTP handler and the service.
- Local specs `orchestrator-http-server` and `analyzer-adapters` list `ru`.
