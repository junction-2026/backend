## MODIFIED Requirements

### Requirement: Technique classifier settings
The registry SHALL accept `execution.adapter: technique_classifier` with `social_max_words` (positive integer, default 80), `technique_min_words` (positive integer, default 40), `strong_margin` (number from 0 to 1 inclusive, default 0.15), `timeout` (default `30s`), the blocks `technique` and `social` and the optional `spans` block. These keys SHALL NOT be accepted on other adapters.

#### Scenario: Defaults and expansion
- **WHEN** an analyzer with `technique` and `social` blocks only is loaded with its variables set
- **THEN** `social_max_words` is 80, `technique_min_words` is 40, `strong_margin` is 0.15, `timeout` is 30 seconds and endpoints and header values are expanded

#### Scenario: Keys on another adapter
- **WHEN** an `http_sync` analyzer sets `social_max_words`
- **THEN** loading fails

## ADDED Requirements

### Requirement: Technique classifier strong margin
`execution.strong_margin` of a `technique_classifier` analyzer SHALL be a number from 0 to 1 inclusive. Loading SHALL fail, naming the file and the analyzer id, when it is not a number or is outside that range, and when another adapter sets it.

#### Scenario: Configured value
- **WHEN** a technique analyzer sets `strong_margin: 0.3`
- **THEN** it loads with a strong margin of 0.3

#### Scenario: Invalid value
- **WHEN** `strong_margin` is 1.5, -0.1 or the string `abc`
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: Other adapter
- **WHEN** a `custom` analyzer sets `strong_margin`
- **THEN** loading fails with an error naming the file and the analyzer id

### Requirement: Technique classifier strong margin documentation
`docs/examples/technique_classifier.yaml` SHALL set `strong_margin: 0.15` under `execution` with a comment, and the README technique section SHALL document the key and its default.

#### Scenario: Example strong margin
- **WHEN** the example is loaded
- **THEN** its strong margin is 0.15
