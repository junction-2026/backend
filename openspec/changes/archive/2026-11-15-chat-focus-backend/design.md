# Design: chat-focus-backend

Shared contract: analysis-chat-api change `chat-focus` @ shared-specs 36b794f7acd8b31b757f00aeefc57c9f27d7f4d3.

## Message layout
`[persona system, Ground system, (Focus system), history...]`. The focus message is added only when at least one
item remains after dedupe, so a request without focus sends byte-identical messages to before.

## Resolution
Read-only over the stored job: AnalyzerRun by `id` (`techniques`, `llm_techniques`, `wikipedia`, `term_context`),
`Job.risk` and `Job.finalization.result` (only when finalization `succeeded`). Index refs must be non-negative
decimal digits only; `passage` splits on the first two `:` so the technique name may contain `:`. Anything that does
not resolve, including unknown kinds, is sent as `{kind, ref, label, found:false, element:null}`.

## Cap
Ground's trim loop is extracted into `fit(render, items, limit)` and reused with limit 16000; only resolved elements
are trimmable, largest first, marked `{"_trimmed":true,"excerpt":...}`. Ground keeps 48000.

## Safety
JSON is encoded with HTML escaping, so `<` and `>` in labels or data become `<`/`>` and the markers occur
once. The persona names the focus markers as data. Logs carry only `focus_count`.
