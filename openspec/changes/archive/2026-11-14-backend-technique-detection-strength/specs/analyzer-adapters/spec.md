## ADDED Requirements

### Requirement: Technique detection strength
After the span step, `technique_classifier` SHALL set on each Detection `margin` = the highest `score - cutoff` of its name over the ModelResults listed in its `models`, rounded half away from zero to 2 decimals; `located` = its final `spans` has at least one entry; `strength` = `strong` when `located` is true or the rounded `margin` is at least the analyzer's `strong_margin`, else `weak`.

#### Scenario: Borderline and clear detections
- **WHEN** `nci-social` (cutoff 0.4) scores `Bandwagon` 0.44, `nci-technique` (cutoff 0.5) scores `Loaded_Language` 0.70 and neither has a span
- **THEN** `Bandwagon` has `margin: 0.04`, `located: false`, `strength: "weak"` and `Loaded_Language` has `margin: 0.2`, `strength: "strong"`

#### Scenario: Margin equal to the strong margin
- **WHEN** a technique scores 0.65 with cutoff 0.5, has no span and `strong_margin` is the default 0.15
- **THEN** its `margin` is 0.15 and its `strength` is `strong`

#### Scenario: Highest margin across models
- **WHEN** `Doubt` scores 0.55 (cutoff 0.5) and 0.5 (cutoff 0.3)
- **THEN** its `margin` is 0.2

#### Scenario: Located detection
- **WHEN** a Detection with margin 0.01 has at least one span
- **THEN** it has `located: true` and `strength: "strong"`

#### Scenario: Span step failed
- **WHEN** `span_status` is `failed` or `timeout`
- **THEN** every Detection has `located: false` and its strength follows its margin only

### Requirement: Technique strength summary
The TechniqueClassification SHALL include `summary: {strong, weak}`, the number of Detections in `detected` with each strength; it SHALL be `{strong: 0, weak: 0}` when `detected` is empty.

#### Scenario: Mixed detections
- **WHEN** `detected` has one `strong` and one `weak` Detection
- **THEN** `summary` is `{"strong": 1, "weak": 1}`

#### Scenario: Nothing detected
- **WHEN** `detected` is empty
- **THEN** `summary` is `{"strong": 0, "weak": 0}`

### Requirement: Technique classifier example strength description
The technique_classifier `llm.description` in `docs/examples/technique_classifier.yaml` SHALL explain `margin`, `located` and `strength` and SHALL say that weak detections are hints at most (borderline score, no phrase located) that on their own must not move a verdict away from `not_detected` and may only lower its confidence.

#### Scenario: Description
- **WHEN** the example's `llm.description` is read
- **THEN** it mentions `margin`, `located`, `strength`, that a weak detection is a hint at most and that it must not move a verdict away from not_detected

### Requirement: Technique strength tests
Stub-only tests SHALL cover the scenarios of Technique detection strength and Technique strength summary, and a test SHALL show that a stored `techniques` result without `margin`, `located`, `strength` or `summary` is served unchanged by `GET /api/v1/jobs/{id}`.

#### Scenario: Offline suite
- **WHEN** `go test ./... -race -count=1` runs without network access
- **THEN** the strength tests pass
