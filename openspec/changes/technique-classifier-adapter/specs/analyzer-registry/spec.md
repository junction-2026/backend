## ADDED Requirements

### Requirement: Technique classifier settings
The registry SHALL accept `execution.adapter: technique_classifier` with `short_max_words` (positive integer, default 80), `max_sentences` (positive integer, default 20), `timeout` (default `30s`) and the blocks `short` and `long`. These keys SHALL NOT be accepted on other adapters.

#### Scenario: Defaults and expansion
- **WHEN** an analyzer with `short` and `long` blocks only is loaded with its variables set
- **THEN** `short_max_words` is 80, `max_sentences` is 20, `timeout` is 30 seconds and endpoints and header values are expanded

#### Scenario: Keys on another adapter
- **WHEN** an `http_sync` analyzer sets `max_sentences`
- **THEN** loading fails

### Requirement: Technique classifier model blocks
Each of `short` and `long` SHALL have `endpoint` (required, non-empty, `${VAR}` expanded), `headers` (`${VAR}` expanded), `threshold` (required, 0 to 1) and optionally `thresholds`, a map from technique name to a number from 0 to 1.

#### Scenario: Override map
- **WHEN** `long.thresholds` is `{Doubt: 0.7}`
- **THEN** the effective threshold of `Doubt` in the long model is 0.7 and of every other technique `long.threshold`

### Requirement: Technique classifier validation
Loading SHALL fail with an error naming the file and the analyzer id when a model block is missing, an endpoint is empty, a threshold is missing or outside 0 to 1, a `thresholds` key is not one of the 18 technique names or its value is outside 0 to 1, or `short_max_words` or `max_sentences` is not positive.

#### Scenario: Threshold out of range
- **WHEN** `long.threshold` is 1.5
- **THEN** loading fails naming the file, the analyzer `techniques` and `execution.long.threshold`

#### Scenario: Unknown technique name
- **WHEN** `short.thresholds` has the key `Nope`
- **THEN** loading fails

### Requirement: Technique classifier top-level keys and bindings
Loading SHALL fail, naming the file and the analyzer id, when `execution.endpoint`, `method`, `handler`, `poll_interval` or `headers` is set, or when `input.bindings` is anything other than `text` bound to `$analyzers.<id>` with `<id>` in `depends_on`.

#### Scenario: Wrong binding
- **WHEN** `input.bindings` is `{text: $input}`
- **THEN** loading fails

### Requirement: Technique classifier placeholders
An enabled technique analyzer with an unset `${VAR}` SHALL fail to load naming the variable and never a value; a disabled one SHALL load.

#### Scenario: Disabled analyzer
- **WHEN** a disabled technique analyzer references unset variables
- **THEN** the registry loads

### Requirement: Technique classifier example
`docs/examples/technique_classifier.yaml` SHALL define the analyzer `techniques` (category `propaganda`, `depends_on: [translation]`, binding `text: "$analyzers.translation"`, endpoints `${TECHNIQUE_SHORT_URL}` and `${TECHNIQUE_LONG_URL}`, header `Authorization: Bearer ${ML_API_TOKEN}`, thresholds 0.3 and 0.5, `llm.include: true` with a description). The README SHALL document it in its own section with the three variables.

#### Scenario: Example loads
- **WHEN** the example is loaded next to an analyzer `translation` with the three variables set
- **THEN** `techniques` is enabled, depends on `translation` and keeps its `llm` block
