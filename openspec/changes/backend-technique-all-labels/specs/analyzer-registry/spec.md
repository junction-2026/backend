## ADDED Requirements

### Requirement: Technique classifier old keys
Loading SHALL fail, naming the file, the analyzer id and the replacement key, when an analyzer sets a key of the earlier technique classifier: `execution.short`, `execution.long`, `execution.short_max_words`, `execution.max_sentences`, or `threshold` or `thresholds` in the `technique` or `social` block.

#### Scenario: Old short and long blocks
- **WHEN** an analyzer sets `execution.short`, then `execution.long`
- **THEN** loading fails naming `execution.social`, then `execution.technique`

#### Scenario: Old threshold
- **WHEN** the `social` block sets `threshold: 0.3` or `thresholds`
- **THEN** loading fails with an error naming `execution.social.cutoff`

#### Scenario: Old word and sentence keys
- **WHEN** an analyzer sets `execution.short_max_words`, then `execution.max_sentences`
- **THEN** loading fails naming `social_max_words` and `technique_min_words`, then `spans.max_sentences`

### Requirement: Technique classifier span block kept
The `spans` block (`sentence_model` with `endpoint`, `headers`, `threshold` and `thresholds`, `llm`, `timeout` and the caps `max_sentences`, `max_phrases_per_technique`, `max_input_chars`) SHALL be accepted and validated as before this change. `threshold` and `thresholds` SHALL stay valid inside `spans.sentence_model`.

#### Scenario: Span block with new model blocks
- **WHEN** an analyzer with `technique` and `social` blocks has a `spans` block whose `sentence_model` sets `threshold: 0.4` and `thresholds: {Doubt: 0.6}`
- **THEN** it loads and the sentence model's effective threshold of `Doubt` is 0.6

#### Scenario: Span block still validated
- **WHEN** `spans.sentence_model.thresholds` has the key `Nope`
- **THEN** loading fails

### Requirement: Technique classifier NCI model blocks
Each of `technique` (the `nci-technique` model) and `social` (the `nci-social` model) SHALL have `endpoint` (required, non-empty, `${VAR}` expanded), `headers` (`${VAR}` expanded) and `cutoff` (optional, 0 to 1, default 0.5 for `technique` and 0.3 for `social`).

#### Scenario: Default cutoffs
- **WHEN** neither block sets `cutoff`
- **THEN** the effective cutoff is 0.5 for `technique` and 0.3 for `social`

#### Scenario: Configured cutoff
- **WHEN** `technique.cutoff` is 0.6
- **THEN** the effective cutoff of `nci-technique` is 0.6

### Requirement: Technique classifier bounds and cutoff validation
Loading SHALL fail with an error naming the file and the analyzer id when a model block is missing, an endpoint is empty, a `cutoff` is outside 0 to 1, `social_max_words` or `technique_min_words` is not positive, or `technique_min_words` is greater than `social_max_words + 1` (a word count would select no model).

#### Scenario: Cutoff out of range
- **WHEN** `technique.cutoff` is 1.5
- **THEN** loading fails naming the file, the analyzer `techniques` and `execution.technique.cutoff`

#### Scenario: Gap between the bounds
- **WHEN** `social_max_words` is 30 and `technique_min_words` is the default 40
- **THEN** loading fails naming `technique_min_words` and `social_max_words`

#### Scenario: Adjacent bounds
- **WHEN** `social_max_words` is 10 and `technique_min_words` is 11
- **THEN** the analyzer loads

### Requirement: Technique classifier example description
The example's `llm.description` SHALL state that a score is the model's confidence in that technique on the English translation, that several labels may be true at once, that a low score is not a technique, that scores of the two models are not comparable or averaged, and that `detected` entries have spans.

#### Scenario: Description
- **WHEN** the example's `llm.description` is read
- **THEN** it mentions the English translation, several labels at once, that a low score is not a technique, `nci-technique`, `nci-social` and spans

## MODIFIED Requirements

### Requirement: Technique classifier settings
The registry SHALL accept `execution.adapter: technique_classifier` with `social_max_words` (positive integer, default 80), `technique_min_words` (positive integer, default 40), `timeout` (default `30s`), the blocks `technique` and `social` and the optional `spans` block. These keys SHALL NOT be accepted on other adapters.

#### Scenario: Defaults and expansion
- **WHEN** an analyzer with `technique` and `social` blocks only is loaded with its variables set
- **THEN** `social_max_words` is 80, `technique_min_words` is 40, `timeout` is 30 seconds and endpoints and header values are expanded

#### Scenario: Keys on another adapter
- **WHEN** an `http_sync` analyzer sets `social_max_words`
- **THEN** loading fails

### Requirement: Technique classifier example
`docs/examples/technique_classifier.yaml` SHALL define the analyzer `techniques` (category `propaganda`, `depends_on: [translation]`, binding `text: "$analyzers.translation"`, `social_max_words: 80`, `technique_min_words: 40`, endpoints `${NCI_TECHNIQUE_URL}` and `${NCI_SOCIAL_URL}`, header `Authorization: Bearer ${ML_API_TOKEN}`, cutoffs 0.5 and 0.3, `llm.include: true`) and a `spans` block. The README SHALL document it in its own section with its variables, keys and defaults.

#### Scenario: Example loads
- **WHEN** the example is loaded next to an analyzer `translation` with its variables set
- **THEN** `techniques` is enabled, depends on `translation`, keeps its `llm` block and has a loaded `spans` block

#### Scenario: Span variables
- **WHEN** the example's `spans` block is read
- **THEN** it uses `${TECHNIQUE_SPAN_URL}`, `${SPAN_LLM_BASE_URL}`, `${SPAN_LLM_API_KEY}` and `${SPAN_LLM_MODEL}`

#### Scenario: Span variable missing
- **WHEN** the example is loaded without `SPAN_LLM_API_KEY`
- **THEN** loading fails naming `SPAN_LLM_API_KEY`

### Requirement: Technique span sentence model settings
`spans.sentence_model` SHALL take `endpoint`, `headers`, `threshold` (required, 0 to 1) and `thresholds` (per-technique overrides), with `${VAR}` expansion of the endpoint and header values like the `technique` and `social` blocks.

#### Scenario: Override map
- **WHEN** `spans.sentence_model` has `threshold: 0.4` and `thresholds: {Doubt: 0.6}`
- **THEN** the effective threshold of `Doubt` is 0.6 and of every other technique 0.4

### Requirement: Technique span validation
Loading SHALL fail with an error naming the file, the analyzer id and the field when `spans` lacks `sentence_model`, `llm` or a positive `timeout`, when `spans.timeout` is not shorter than `execution.timeout`, when the sentence model has no endpoint, a missing `threshold` or one outside 0 to 1, a `thresholds` key that is not a technique name or a value outside 0 to 1, when a cap is not a positive integer or when `llm.extra_body` sets `model` or `messages`.

#### Scenario: Span deadline too long
- **WHEN** `execution.timeout` is 30s and `spans.timeout` is 30s
- **THEN** loading fails naming `execution.spans.timeout`

#### Scenario: Unknown technique override
- **WHEN** `spans.sentence_model.thresholds` has the key `Nope`
- **THEN** loading fails

## REMOVED Requirements

### Requirement: Technique classifier model blocks
**Reason**: The `short` and `long` blocks with `threshold` and `thresholds` are replaced by "Technique classifier NCI model blocks" (`technique` and `social` with one `cutoff` each).
**Migration**: Rename `long` to `technique` and `short` to `social` and replace `threshold`/`thresholds` with `cutoff`; loading names each replacement.

### Requirement: Technique classifier validation
**Reason**: Replaced by "Technique classifier bounds and cutoff validation" for the new keys.
**Migration**: See "Technique classifier old keys".
