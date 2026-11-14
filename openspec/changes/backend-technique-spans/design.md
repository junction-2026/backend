# Design: backend-technique-spans

## Placement

The span step runs inside the `technique_classifier` adapter after the document result is built
(`techniqueSpans` in `internal/adapter/technique_spans.go`). It receives the `detected` list, `Job.input.content`
(`Inputs[job_content]`) and the translation's `source_language`, and returns the span members. Detections inside
`sentences[].techniques` keep the old type and carry no `spans`.

## Sentence method (English)

`splitSentences` already returns trimmed sentences with UTF-16 offsets into the text it is given; it is run on
`Job.input.content`, so its offsets are the span offsets. The request and answer validation reuse
`parseTechniqueResponse` with the sentence count as the expected list count (no full-text entry).

## Phrase method (other languages)

The span LLM reuses `adapter.LLM` (OpenAI-compatible request, one retry on 429/5xx/connection error, reply decoding,
provider error redaction). The registry builds an llm-style `Execution` (`Spans.Exec`, `Adapter:
technique_classifier`) from the `spans.llm` block and loads it with the existing `loadLLM` (expansion, prompt file,
response format); `LLM` selects `TechniqueSpanSchema` for that adapter. The reply is decoded into
`{phrases: [{technique: string, text: string}]}`; any other shape is unreadable. Candidates are matched with
`strings.Index` on the submitted content: nothing is trimmed, case-folded or normalized. Content longer than
`max_input_chars` runes is cut at a space before it is sent (`spans_truncated: true`); candidates are still matched
against the full content.

## Failure isolation

The step runs under `context.WithTimeout(ctx, spans.timeout)`. If the analyzer's own ctx is done, its error is
returned (run `timeout` / `cancelled` as before). Otherwise an error gives `span_status: timeout` when the span
context's deadline elapsed, else `failed`; spans are emptied, `spans_truncated` is false and the cause is logged
once with `slog.Warn` through `techniqueRedactor`, which now also covers the sentence model's header values and the
span LLM's api_key and headers. The registry requires `spans.timeout < execution.timeout`.

## Without a spans block

Nothing extra is requested: `span_method` follows the language, `span_status` is `ok`, every `detected[].spans` is
`[]` (feature off).

## Risks

- The sentence and document models can disagree; an English detection may have `spans: []`.
- LLM near-matches are rejected by design.
- The LLM's single retry backs off 1s; with a short `spans.timeout` a refused connection ends as `timeout`.
