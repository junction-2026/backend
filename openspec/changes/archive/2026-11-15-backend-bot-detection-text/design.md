# Design

- The adapter mirrors `propaganda_detection` (fixed `{"text": ...}` body, `Executor.send`, status-only errors) but takes the
  text only from the `content` binding (`$input`, the default), never from `$analyzers.*`; the registry rejects other bindings.
- Response: the first-level `result` member is unwrapped; an object, an array of objects or an array of arrays of objects
  is flattened. The highest-scoring element is the model's top label. `LABEL_1` is bot / generated, `LABEL_0` human; any
  other label, a duplicate label, a missing or non-numeric score or a score outside 0..1 is `invalid_output`.
- `score` is P(bot_or_generated): the `LABEL_1` probability when that label is listed, else `1 - p` of `LABEL_0`
  (rounded to 10 decimals to hide float noise). `model_label`/`model_score` carry the raw top label and probability.
- Error messages never repeat response content; they pass through the technique-style header/token redactor anyway.
