## ADDED Requirements

### Requirement: Technique classifier model results
The result SHALL contain `words` and `results`: one ModelResult `{model, cutoff, scores, latency_ms}` per selected model, `nci-technique` before `nci-social`, with `cutoff` the effective cutoff of that model's block and `latency_ms` the service's value or null. Scores of the two models SHALL NOT be averaged or merged.

#### Scenario: No averaging
- **WHEN** both models run and `Doubt` scores 0.6 from `nci-technique` and 0.2 from `nci-social`
- **THEN** each ModelResult has its own `Doubt` score and no other `Doubt` score is in the result

#### Scenario: Configured cutoff echoed
- **WHEN** `technique.cutoff` is 0.6
- **THEN** the `nci-technique` ModelResult and each of its scores have `cutoff: 0.6`

### Requirement: Technique classifier score list
`scores` SHALL hold one TechniqueScore `{label, display, score, model, cutoff, above_cutoff, known}` per known technique name in the order of the names list, then one per unfamiliar label in the model's order. `above_cutoff` SHALL be `score >= cutoff`; below-cutoff scores SHALL be present. An unfamiliar label SHALL have `display` equal to the label and `known: false`.

#### Scenario: Cutoff equality
- **WHEN** the `nci-social` cutoff is 0.3 and `Doubt` scores 0.3
- **THEN** its TechniqueScore has `above_cutoff: true`

#### Scenario: Just below
- **WHEN** the `nci-technique` cutoff is 0.5 and `Doubt` scores 0.49
- **THEN** its TechniqueScore is present with `score: 0.49` and `above_cutoff: false`

#### Scenario: Unfamiliar label
- **WHEN** a model returns `Appeal_to_Pity` with 0.7 before the 18 known labels
- **THEN** its `scores` start with `Loaded_Language` and end with `{label: "Appeal_to_Pity", display: "Appeal_to_Pity", score: 0.7, known: false, above_cutoff: true}`

### Requirement: Technique classifier removed members
The result SHALL NOT contain `model`, `threshold`, `thresholds`, `techniques`, `max_score`, a top-level `latency_ms`, `sentences`, `offsets_in` or `sentences_truncated`, and a Detection SHALL NOT contain `score`.

#### Scenario: Top-level members
- **WHEN** a run succeeds
- **THEN** its result's top-level members are `words`, `results`, `detected`, `span_method`, `span_status` and `spans_truncated`

### Requirement: Technique classifier detected
`detected` SHALL list a Detection `{name, display, explanation, models, spans}` for each known technique name with `above_cutoff: true` in at least one ModelResult, in the order of the names list, with `models` the `model` values of those ModelResults in `results` order, and no `score`. Unfamiliar labels SHALL NOT be in `detected`. This list SHALL be passed to the span step, which adds `spans`, `span_method`, `span_status` and `spans_truncated` as before.

#### Scenario: Above one cutoff
- **WHEN** both models run with default cutoffs and `Loaded_Language` scores 0.4 from both
- **THEN** `detected` has `Loaded_Language` with `models: ["nci-social"]`

#### Scenario: Nothing above cutoff
- **WHEN** no known label reaches its model's cutoff
- **THEN** `detected` is `[]` and no span request is sent

### Requirement: Technique classifier job content
The adapter SHALL receive `Job.input.content` as the `job_content` input only for the span step. Classification requests to `nci-technique` and `nci-social` SHALL carry only `translated_text`.

#### Scenario: German job
- **WHEN** a German job's translation is English and both models are selected
- **THEN** each model receives exactly one request whose `text` is `[translated_text]` and neither receives the German content

### Requirement: Technique classifier model routing
`words` SHALL be `len(strings.Fields(translated_text))`. The adapter SHALL select `nci-technique` when `words >= technique_min_words` or `words > social_max_words`, and `nci-social` when `words <= social_max_words`, and SHALL NOT consult `input.source`.

#### Scenario: Boundaries with defaults
- **WHEN** the text has 39, 40, 80 and 81 words with the defaults 80 and 40
- **THEN** 39 words select `nci-social`, 40 and 80 words both models and 81 words `nci-technique`

#### Scenario: Platform ignored
- **WHEN** two jobs with the same translation differ only in `input.source.platform`
- **THEN** the results are identical

### Requirement: Technique classifier model requests
The adapter SHALL send exactly one `POST {"text": [translated_text]}` to each selected model's endpoint with that block's configured headers, concurrently when both are selected and under the analyzer's timeout, and SHALL NOT retry, send sentence batches or call an unselected model.

#### Scenario: One model selected
- **WHEN** only `nci-social` is selected
- **THEN** it receives one request with one text and `nci-technique` receives none

#### Scenario: Both selected
- **WHEN** both models are selected
- **THEN** each receives one request with one text

## MODIFIED Requirements

### Requirement: Technique classifier response
The response SHALL be `{"result": [[{"label", "score"}, ...]], "latency_ms"?}` with one list holding each of the 18 known technique names exactly once and numeric scores from 0 to 1. Unfamiliar labels SHALL be kept. Anything else (not an object, no `result`, a different number of lists, a missing known label, a duplicate, missing, empty or non-string label, a score that is not a number or outside 0 to 1, a `latency_ms` that is not a non-negative number) SHALL fail with `invalid_output`.

#### Scenario: Unnamed labels
- **WHEN** the labels are `LABEL_0` to `LABEL_17`
- **THEN** the analyzer fails with `invalid_output`

#### Scenario: Extra label
- **WHEN** the list holds the 18 known labels and `Appeal_to_Pity`
- **THEN** the analyzer succeeds

### Requirement: Technique classifier failures
A non-2xx answer or a connection error SHALL fail with `adapter_error`, an elapsed timeout with `timeout` and job cancellation with `cancelled`, without retry and without fallback to the other model. When both models are selected and one fails, the run SHALL fail with that model's error and no result; the other call SHALL be cancelled.

#### Scenario: Service unavailable
- **WHEN** only `nci-technique` is selected and it answers HTTP 503
- **THEN** the analyzer fails with `adapter_error` after one request and `nci-social` receives none

#### Scenario: One of two fails
- **WHEN** both models are selected and `nci-social` answers HTTP 503
- **THEN** the analyzer fails with `adapter_error` and no result, without waiting for a slow `nci-technique`

### Requirement: Technique display names
The adapter SHALL have a built-in map from each of the 18 technique names to a non-empty short display name and a non-empty one-line explanation (for example `Loaded_Language` to `Loaded language` and `Words chosen to trigger strong emotions`). Every TechniqueScore SHALL carry `display` (the label itself for an unfamiliar label), every Detection `display` and `explanation`.

#### Scenario: Map complete
- **WHEN** the map is checked against the 18 names
- **THEN** every name has a display name and an explanation

### Requirement: Technique classifier hides credentials
Error messages and logs SHALL NOT contain any configured header value or token of either model, including when a service echoes them in a response body. Messages SHALL NOT repeat response content.

#### Scenario: Echoed token
- **WHEN** either model answers 401 with a body that repeats the bearer tokens of both blocks
- **THEN** `error.message` contains neither token

### Requirement: Technique classifier tests
Tests SHALL use stub HTTP services and need no real endpoint or token. They SHALL cover routing bounds, the request body, cutoffs, unfamiliar labels, no averaging, every `invalid_output` case, failures without fallback, partial failure, a non-English job, credential redaction, the display map, registry validation of old keys and of the bound relation, and the example. Span and `propaganda_detection` tests SHALL keep passing.

#### Scenario: Suite runs offline
- **WHEN** `go test ./...` runs without network access
- **THEN** the technique classifier tests pass

#### Scenario: Bounds and cutoffs covered
- **WHEN** the routing and cutoff tests run with default settings
- **THEN** they check 39, 40, 80 and 81 words, a score equal to the cutoff and a score of 0.49 against 0.5

### Requirement: Technique span sentence model request
The adapter SHALL send one `POST {"text": [sentence 1..N]}` with the sentence model's headers and validate the answer like a classification answer, one list per sentence. A sentence SHALL be a span of each technique in `detected` it scores at or above the sentence model's effective threshold for it; other labels SHALL be ignored. A non-2xx answer, a connection error or any validation failure SHALL give `span_status` `failed`.

#### Scenario: Undetected technique
- **WHEN** a sentence scores `Doubt` 0.95 and `Doubt` is not in `detected`
- **THEN** no `Doubt` span exists and `detected` is unchanged

#### Scenario: Override threshold
- **WHEN** the sentence model's `Name_Calling,Labeling` override is 0.6 and a sentence scores it 0.55
- **THEN** that sentence is not a span of `Name_Calling,Labeling`

### Requirement: Technique span deadline
The span step SHALL run under its own deadline `spans.timeout` inside the analyzer's context. When it elapses `span_status` SHALL be `timeout`. A failed or timed-out span step SHALL leave the run `succeeded` with every `detected[].spans` `[]` and `results` and `detected` membership and `models` equal to the result without spans. When the analyzer's own timeout elapses the run SHALL fail with `timeout` as before. No span request SHALL be sent when `detected` is empty.

#### Scenario: Slow LLM
- **WHEN** the span LLM does not answer within `spans.timeout`
- **THEN** the run succeeds with `span_status` `timeout`

#### Scenario: Analyzer timeout during the span step
- **WHEN** the analyzer's context ends while the span LLM is waiting
- **THEN** the run fails with `timeout`

## REMOVED Requirements

### Requirement: Technique classifier routing
**Reason**: The `short`/`long` choice is replaced by "Technique classifier model routing" (one or both NCI models by two word bounds).
**Migration**: Configure `social_max_words` and `technique_min_words`.

### Requirement: Technique classifier request
**Reason**: Sentence batches are no longer sent; replaced by "Technique classifier model requests" (one text per selected model).
**Migration**: None for clients; ML services receive `{"text": [translated_text]}`.

### Requirement: Technique classifier sentences
**Reason**: The NCI models classify the whole translated text; sentence batches on the translation are no longer sent (shared change `technique-all-labels`).
**Migration**: Spans in the user's text come from the span step.

### Requirement: Technique classifier scores
**Reason**: Replaced by "Technique classifier model results" and "Technique classifier detected" (one cutoff per model, no per-technique thresholds, no top-level score summary).
**Migration**: Read `results[].scores[]` and `detected[].models`.

### Requirement: Technique classifier sentence results
**Reason**: `sentences` and `sentences_truncated` are removed from the shared result.
**Migration**: Use `detected[].spans`.

### Requirement: Technique classifier offsets
**Reason**: `offsets_in` and sentence offsets are removed from the shared result.
**Migration**: Span offsets always index `Job.input.content`.
