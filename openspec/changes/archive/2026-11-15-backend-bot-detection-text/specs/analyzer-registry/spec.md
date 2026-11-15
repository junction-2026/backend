## ADDED Requirements

### Requirement: bot_detection adapter configuration
The registry SHALL accept the adapter `bot_detection` with `execution.endpoint` (required when the analyzer is enabled), optional `execution.headers` and `execution.timeout`. It SHALL reject `execution.method` other than POST, `execution.handler`, `execution.poll_interval` and any binding other than `content: $input`, failing start-up like other adapters.

#### Scenario: Missing endpoint
- **WHEN** an enabled `bot_detection` analyzer has no endpoint
- **THEN** loading the registry fails naming `execution.endpoint`

#### Scenario: Binding from a dependency
- **WHEN** an analyzer sets `input.bindings: {content: $analyzers.translation}`
- **THEN** loading the registry fails

### Requirement: bot_detection registry example
`docs/examples/bot_detection.yaml` SHALL define analyzer `bot_detection` (name `Bot detection`, category `bot_detection`, `enabled: false`, no `depends_on`, adapter `bot_detection`, endpoint `${BOT_DETECTION_URL}`, timeout 30s, `llm.include: true`).

#### Scenario: Example loads
- **WHEN** the example is loaded with `BOT_DETECTION_URL` set and `enabled: true`
- **THEN** the registry accepts it

### Requirement: bot_detection llm description
The `llm.description` of the example SHALL state that it classifies this single text only and is not an account verdict, that the multilingual model sees the original text and not the translation, that `score` is P(bot_or_generated) and that it is not propaganda evidence.

#### Scenario: Description
- **WHEN** the example is loaded
- **THEN** the description contains those statements

### Requirement: bot_detection risk-score mapping documented
`config/risk_score/risk_score.yaml` and the example SHALL document `single_text_score: score` for this adapter, still commented out, so default risk scores are unchanged.

#### Scenario: Default configuration
- **WHEN** the default risk configuration is loaded
- **THEN** it has no `bot_detection` mapping
