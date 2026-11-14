## Purpose
Backend implementation of the Manipulation Risk Score: its configuration file, the bot-detector result mapping, the pure computation and the fixed English texts, implementing the shared manipulation-risk-score capability.

## ADDED Requirements

### Requirement: Risk configuration file
The server SHALL read the risk configuration from the YAML file named by `RISK_SCORE_CONFIG`; with the variable unset every value SHALL be the shared default. Members absent from the file SHALL keep their default; unknown members SHALL be an error.

#### Scenario: No file
- **WHEN** `RISK_SCORE_CONFIG` is unset
- **THEN** every RiskScore echoes the shared defaults in `config`

#### Scenario: Partial file
- **WHEN** the file only sets `dampening_factor: 0.5`
- **THEN** new RiskScores have `config.dampening_factor` 0.5 and the defaults elsewhere

### Requirement: Risk weight and band validation
Start-up SHALL fail with an error naming the member when a weight is negative, when the weights of a set do not sum to 1 or give intensity or confirmation no positive weight, or when `bands` is empty, does not start at `min` 0 or has `min` values that are not strictly ascending within 0..1.

#### Scenario: Invalid bands
- **WHEN** the file sets `bands` starting at `min: 0.1`
- **THEN** loading fails with an error naming `bands` and the server does not start

#### Scenario: Negative weight
- **WHEN** the file sets a negative impact weight
- **THEN** loading fails with an error naming that member

### Requirement: Risk threshold and count validation
Start-up SHALL fail with an error naming the member when `detection_threshold`, `dampening_threshold`, `dampening_factor` or `driver_min_contribution` lies outside 0..1, or when `reference_technique_count`, `weighted_sum_reference`, `min_author_posts` or `short_text_max_words` is not positive.

#### Scenario: Out-of-range threshold
- **WHEN** the file sets `detection_threshold: 1.5`
- **THEN** loading fails with an error naming `detection_threshold`

#### Scenario: Non-positive count
- **WHEN** the file sets `reference_technique_count: 0`
- **THEN** loading fails with an error naming `reference_technique_count`

### Requirement: Bot detection mapping
The risk configuration SHALL have an optional `bot_detection` mapping naming, as dot-separated paths into the `bot_detection` AnalyzerRun result (a numeric segment indexes an array), the members `author_score`, `author_posts` and `single_text_score`. A path that is not configured, does not resolve, or resolves to a value of the wrong type or outside its range (scores 0..1, post count a non-negative integer) SHALL count as absent. The mapping SHALL NOT appear in the RiskScore.

#### Scenario: Mapped result
- **WHEN** the mapping is `author_score: author.score`, `author_posts: author.posts` and the result is `{"author": {"score": 0.9, "posts": 120}}`
- **THEN** the author score is 0.9 and the post count 120

#### Scenario: No mapping
- **WHEN** no mapping is configured and the `bot_detection` run succeeded for a job with author context
- **THEN** `dimensions.amplification` is unavailable with `no_bot_score`

#### Scenario: Out-of-range value
- **WHEN** the mapped single-text score is 1.5
- **THEN** it counts as absent

### Requirement: Pure computation
The RiskScore SHALL be computed by `risk.Compute`, a pure function of the job snapshot and the settings that performs no I/O and reads no clock. Its unit tests SHALL reproduce every scenario of the shared manipulation-risk-score capability (all numeric values at 4 decimals, the band boundary and the dampened, missing-input and caveat-order scenarios).

#### Scenario: Equal inputs
- **WHEN** the same job snapshot and settings are computed twice
- **THEN** the two RiskScores are equal and equal when marshalled

### Requirement: Fixed English texts
`summary`, driver texts and caveat texts SHALL be produced from fixed English templates held in one file, `internal/risk/texts.go`, filled only with a band id or a technique `display`. A test SHALL assert that every default template text contains no digit and none of `nci-`, `classifier`, `model`.

#### Scenario: Template check
- **WHEN** every band, driver kind and caveat id of the defaults is rendered
- **THEN** no text contains a digit, `nci-`, `classifier` or `model`
