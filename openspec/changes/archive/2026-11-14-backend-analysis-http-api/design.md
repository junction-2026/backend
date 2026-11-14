# Design: backend analysis HTTP API

## Decisions

### D1. The handlers depend on a small `Service` interface
`internal/httpapi.Service` lists the five engine methods the API needs (`Create`, `Get`,
`List`, `Cancel`, `Subscribe`); `*engine.Engine` satisfies it and tests substitute a
fake. The analyzer listing reads the registry directly through an explicit
`AnalyzerItem` struct, so execution settings cannot leak by adding a field to a
registry type.

### D2. Own route table, own 404/405
`net/http.ServeMux` answers unmatched paths and methods in plain text. Routes are
registered per path with a method map, and `/` is a catch-all, so every 404 and 405 is
an error envelope (with an `Allow` header for 405).

### D3. Request validation by hand, in two steps
The body is parsed into generic JSON first: a syntax error, trailing data or an empty
body is 400 `invalid_request`. Then each member is validated with its dotted path
(`input.content`, `input.source.url`, ...); all violations are collected into
`details.fields` for one 422. `source.metadata` is kept as raw JSON. Unknown members are
ignored simply because they are never read.

### D4. Cursor = base64url(`<created_at RFC3339Nano>|<id>`)
The list is the engine's newest-first order; the page starts at the first job strictly
after the cursor in that order, so jobs created or evicted between pages never cause
duplicates. A cursor that does not decode or parse is 400.

### D5. WebSocket delivery rides on the engine's bus
The bus already numbers events, takes the snapshot atomically and drops a subscriber
whose buffer (256, set by `EventBuffer`) overflows. The connection loop forwards frames
and, when the channel closes, maps `Err()` to close code 4001 (`slow_consumer`) or 1000.
A writer blocked on a stalled client is bounded by the 10s write timeout. Pings go out
every 25s from the same loop; a goroutine started with `CloseRead` discards client
messages and answers control frames.

### D6. Shutdown order
SIGTERM/SIGINT: refuse new WebSocket upgrades and close open ones with 1001, then
`http.Server.Shutdown` (it does not track hijacked connections, so the WebSocket
handler is waited for separately), then `Engine.Close()` which aborts running analyzers.
The whole sequence is bounded by 10s.

### D7. Logging
`log/slog` JSON to stdout. Request logs carry method, route and status only (never
query, headers or bodies). Job and analyzer lifecycle lines carry ids, statuses and error
*codes*; never results, error messages (they may contain endpoint details) or URLs.
