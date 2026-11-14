# Design: Ask VEIL chat (backend)

Shared contract: `analysis-chat-api` @ 5f4e9e4789a043c2ee8620f54fc3da58cea1cb86. Based on backend @ e8c46a1.

## Decisions
1. **Package `internal/chat`** holds everything except HTTP parsing: `Service` (config, limiter, upstream call),
   `Window`, `Ground`, the embedded `persona.txt`. The handler in `internal/httpapi/chat.go` validates, reads the job
   with `Service.Get` (nothing else: no Create/Cancel/Subscribe) and maps `chat` errors to the error envelope.
2. **Check order**: body (413/400/422) → job (404) → status (409) → chat configured (503) → limits (429) → upstream.
3. **Grounding message is a second `system` message.** Everything the job contributes is JSON-encoded with Go's
   default HTML escaping, so the literal delimiter `<<<` can never occur inside the data block; the persona prompt
   says the block is data. Trimming works on the encoded size: the largest item (analyzer result or input content) is
   replaced by `{"_trimmed":true,"excerpt":"<head of the original JSON>"}` until the message fits 48000 code points.
   shortcut: the fixed skeleton (ids, names, statuses) is never trimmed; fine for the registry size, revisit with hundreds of analyzers.
4. **Limiter**: one mutex guarding a per-job map (token bucket of `CHAT_RATE_PER_MINUTE` tokens, in-flight flag) and a
   global in-flight counter. A request is admitted atomically or rejected without consuming a token. Idle buckets are
   swept when the map exceeds 1024 entries.
5. **Upstream failures** collapse into two sentinel errors (`ErrUnavailable`, `ErrTimeout`); upstream text is never
   propagated. The reply and the upstream error path additionally replace the API key and base URL by `[redacted]`.
6. **Logging**: `chat` line with `job_id`, `outcome`, `duration_ms` only.
7. **Configuration**: an absent key keeps the server running with chat answering 503; an invalid value or an
   unreadable `CHAT_API_KEY_FILE` is a start-up error naming the variable.
