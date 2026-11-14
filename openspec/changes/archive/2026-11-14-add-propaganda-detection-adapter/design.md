# Design

- The adapter reads `normalized_content` from the default inputs (`{"content": {..., "normalized_content"}}`). Bindings are rejected at load time, so that shape is guaranteed; a job input without it fails with `adapter_error`.
- It reuses `Executor.send` (headers, correlation headers, no-redirect client, URL-free messages, context mapping) and `readObject`; only the body and response validation are specific.
- Validation is a hand-written check, not a JSON Schema: a user `output.schema` stays applicable afterwards (the engine applies it to the stored result), so the two never conflict.
- Registry checks that depend on "was it set" (`method`, `poll_interval`) run before defaults are applied.
- Redirects: the shared no-redirect client returns the 3xx response, which fails as a non-2xx status.
