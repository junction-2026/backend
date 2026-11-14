## Context

`adapter.LLM` is the only path to the provider (finalizer, `topic_extraction`, technique spans). Before this change it returned the decoded object and usage; callers validated afterwards, so a schema failure could not be retried inside the adapter.

## Decisions

1. **Callers pass a `check` function.** `LLM(ctx, def, user, check)` runs the caller's built-in checks and configured schema on each decoded reply, so the adapter knows a `schema` failure and can retry it. The closure keeps the normalized result, so nothing is parsed twice. Spans pass no check (their shape errors stay non-retried, as before).
2. **Reply retry is separate from the transport retry.** Each of the two requests may itself get the single 429/5xx/connection retry, so a call makes at most one of each kind.
3. **Retry reasons.** `length` for `finish_reason: length`; `invalid_json` for empty content or content that is not a JSON object after unfence; `schema` when `check` fails. Missing `choices`, refusals, other finish reasons (e.g. `content_filter`), non-2xx answers, timeouts and cancellation are not retried.
4. **Reason in the retry prompt** is the first reason's message cut before the first `": "` (after which a JSON-schema error may quote reply values) and to 120 characters, so the failed output is never echoed.
5. **Time budget.** The retry is sent only when the context deadline is at least `retry_min_remaining` away (or there is none); it runs under the same context.
6. **Usage summing** uses exact decimal addition (`math/big.Rat`) so `0.0012 + 0.0010` is `0.0022`.
7. **Logging** goes to `slog.Default()` like the technique-span warning: analyzer id, reason and model only.

## Risks / Trade-offs

- Wrong YAML types for the new keys fail load as decode errors naming the line (as for every typed key), not the field.
- A retry costs one more paid request and some latency; bounded by one retry and the time budget.
