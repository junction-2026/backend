## ADDED Requirements

### Requirement: UI block
An analyzer entry SHALL accept an optional `ui` block with `hidden` (boolean, default false) and `activity_key` (string matching the analyzer id pattern, default the analyzer id). Unknown keys in `ui` SHALL be rejected at load like other unknown registry keys, naming the file and the analyzer id. The `ui` block SHALL NOT appear in any response of `GET /api/v1/analyzers`.

#### Scenario: Defaults
- **WHEN** an analyzer has no `ui` block
- **THEN** it is not hidden and its activity key is its id

#### Scenario: Custom key
- **WHEN** an analyzer sets `ui: {activity_key: translation}`
- **THEN** its activity key is `translation`

#### Scenario: Unknown ui key
- **WHEN** an analyzer sets `ui: {colour: red}`
- **THEN** loading fails naming the file

#### Scenario: Not exposed
- **WHEN** an analyzer sets `ui: {hidden: true}` and a client calls `GET /api/v1/analyzers`
- **THEN** the item has no `ui` member

### Requirement: text_stats hidden by default
The built-in `text_stats` analyzer SHALL be hidden unless its registry entry sets `ui.hidden: false`.

#### Scenario: Default and override
- **WHEN** `text_stats` is loaded without `ui`, then with `ui: {hidden: false}`
- **THEN** it is hidden in the first case and visible in the second
