## ADDED Requirements

### Requirement: Technique classifier input
The `technique_classifier` adapter SHALL read `translated_text` and `source_language` from the bound `translation` result. A missing result, or a `translated_text` that is missing, not a string or blank, SHALL fail with `adapter_error` and no request.

#### Scenario: Unreadable translation
- **WHEN** the bound result has no `translated_text`
- **THEN** the analyzer fails with `adapter_error` and the classifier receives no request

#### Scenario: Translation failed
- **WHEN** `translation` failed
- **THEN** `techniques` is skipped with reason `dependency_failed` and the classifier receives no request

### Requirement: Technique classifier routing
`words` SHALL be the number of whitespace-separated tokens of `translated_text`. The adapter SHALL use the `short` model when `words` is at most `short_max_words` and the `long` model otherwise, and SHALL NOT consult `input.source`.

#### Scenario: Boundary
- **WHEN** `short_max_words` is 80 and the text has 80 words, then 81 words
- **THEN** the first uses the `short` model and the second the `long` model

#### Scenario: Platform ignored
- **WHEN** two jobs with the same translation differ only in `input.source.platform`
- **THEN** the results are identical

### Requirement: Technique classifier sentences
The adapter SHALL split `translated_text` at `.`, `!`, `?` (runs included) and newlines, trim whitespace, keep each sentence's start and end offsets in UTF-16 code units and drop fragments with fewer than 3 words. Only the first `max_sentences` sentences SHALL be scored; `sentences_truncated` SHALL be true when more existed.

#### Scenario: Fragments dropped
- **WHEN** the text is `Wake up. They are LYING to you.`
- **THEN** the only sentence is `They are LYING to you.` at 9 to 31

#### Scenario: Truncation
- **WHEN** `max_sentences` is 2 and the text has 3 sentences
- **THEN** 2 sentences are scored and `sentences_truncated` is true

### Requirement: Technique classifier request
The adapter SHALL send exactly one POST per job to the chosen model's endpoint with that block's configured headers and the body `{"text": [full text, sentence 1, ..., sentence N]}`, and SHALL NOT retry or call the other model. When the text yields at most one sentence and it equals the full text, or none, only the full text SHALL be sent.

#### Scenario: Batch body
- **WHEN** a text with two qualifying sentences is classified
- **THEN** the service receives one request whose `text` has three items

#### Scenario: Single sentence
- **WHEN** the text is one sentence
- **THEN** `text` has one item

### Requirement: Technique classifier response
The response SHALL be `{"result": [[{"label", "score"} x 18], ...], "latency_ms"?}` with one list per text sent, each holding the 18 technique names exactly once with numeric scores from 0 to 1. Anything else (not an object, no `result`, a different number of lists, a missing, unknown or duplicate label, a score that is not a number or outside 0 to 1, a `latency_ms` that is not a non-negative number) SHALL fail with `invalid_output`.

#### Scenario: Unnamed labels
- **WHEN** the labels are `LABEL_0` to `LABEL_17`
- **THEN** the analyzer fails with `invalid_output`

### Requirement: Technique classifier failures
A non-2xx answer or a connection error SHALL fail with `adapter_error`, an elapsed timeout with `timeout` and job cancellation with `cancelled`, without retry and without calling the other model.

#### Scenario: Service unavailable
- **WHEN** the chosen model answers HTTP 503
- **THEN** the analyzer fails with `adapter_error` after one request and the other model receives none

### Requirement: Technique classifier scores
The result SHALL contain `techniques` (18 entries, score descending, each with `display`), `thresholds` (the effective threshold of each technique: the model's override or else its `threshold`), `detected` (score at or above the effective threshold, score descending, with `display` and `explanation`), `max_score`, `model`, `words`, `threshold` (the chosen model's default) and `latency_ms` (the service's value or null).

#### Scenario: Same scores, different model
- **WHEN** `Loaded_Language` scores 0.4, short threshold is 0.3 and long threshold is 0.5
- **THEN** a short result detects it and a long result does not

#### Scenario: Override
- **WHEN** the long model has an override of 0.7 for `Doubt` and `Doubt` scores 0.6
- **THEN** `thresholds.Doubt` is 0.7 and `Doubt` is not detected

### Requirement: Technique classifier sentence results
The result SHALL contain `sentences` (scored sentences with at least one detection, ordered by `start`, `techniques` score descending), `offsets_in` and `sentences_truncated`, as defined by the shared change `technique-classifier-result`.

#### Scenario: No sentence over threshold
- **WHEN** no scored sentence reaches a threshold
- **THEN** `sentences` is an empty array

### Requirement: Technique classifier offsets
Offsets SHALL be UTF-16 code units. When `source_language` is `en`, each reported sentence's text SHALL be searched in `Job.input.content` from left to right, each search starting at the end of the previous match; when all are found the offsets index `Job.input.content` and `offsets_in` is `original`. Otherwise the offsets index `translated_text` and `offsets_in` is `translation`.

#### Scenario: English
- **WHEN** an English job's content is `Wake up. They are LYING to you.` and the second sentence is detected
- **THEN** `offsets_in` is `original` and `content.slice(start, end)` equals `text`

#### Scenario: German or sentence not verbatim
- **WHEN** the source language is `de`, or an English sentence is not found verbatim in the content
- **THEN** `offsets_in` is `translation` and `translated_text.slice(start, end)` equals `text`

### Requirement: Technique display names
The adapter SHALL have a built-in map from each of the 18 technique names to a non-empty short display name and a non-empty one-line explanation (for example `Loaded_Language` to `Loaded language` and `Words chosen to trigger strong emotions`). Every TechniqueScore SHALL carry `display`, every Detection `display` and `explanation`.

#### Scenario: Map complete
- **WHEN** the map is checked against the 18 names
- **THEN** every name has a display name and an explanation

### Requirement: Technique classifier hides credentials
Error messages and logs SHALL NOT contain any configured header value or token, including when the service echoes them in a response body. Messages SHALL NOT repeat response content.

#### Scenario: Echoed token
- **WHEN** the service answers 401 with a body that repeats the bearer token
- **THEN** `error.message` does not contain the token

### Requirement: Technique classifier tests
Tests SHALL use stub HTTP services and need no real endpoint or token. They SHALL cover routing boundaries, the batch body, fragment dropping and truncation, thresholds and overrides, offsets for English and German including non-ASCII and astral text, every `invalid_output` case, 5xx and timeout without fallback, the `dependency_failed` skip, credential redaction, display map completeness and loading of the example.

#### Scenario: Suite runs offline
- **WHEN** `go test ./...` runs without network access
- **THEN** the technique classifier tests pass
