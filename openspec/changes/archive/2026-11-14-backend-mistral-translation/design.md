# Design

- **Request**: the adapter builds its own request (not `send`) so no `X-Analysis-*` headers reach the provider and
  `Authorization: Bearer <api_key>` is set after the configured headers. It uses the shared no-redirect client.
- **Truncation**: by Unicode code points; the cut is made at the last whitespace at or before the limit,
  else hard. Simple and deterministic; no chunking (a later change if needed).
- **Reply validation**: `choices[0].message.content` must be a JSON object with a non-empty `translated_text`;
  `finish_reason: length` is an error because the translation would be cut off.
- **Messages**: fixed strings plus the HTTP status; never the key, endpoint or provider body.
- **Propaganda text**: `adapter.Inputs` already resolves `$analyzers.<id>` to the stored result; the adapter reads
  `translated_text` from it and never falls back to `normalized_content`.
- **Parallel work**: junction-2026/project#6 (`llm` adapter) also edits `registry.go` and the analyzer-registry
  spec; edits here are additive and the MODIFIED requirement text is rebased onto main if #6 merges first.
