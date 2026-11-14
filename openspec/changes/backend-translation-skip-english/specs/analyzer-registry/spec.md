## MODIFIED Requirements

### Requirement: Mistral translation settings
The `mistral_translation` adapter SHALL take `endpoint` (default `https://api.mistral.ai/v1/chat/completions`), `api_key` (required), `model` (default `mistral-small-latest`), `timeout` (default `60s`), `max_input_chars` (positive integer, default `20000`) and `headers`, and the English skip settings. `method`, `handler`, `poll_interval` and `input.bindings` SHALL be rejected for it.

#### Scenario: Mistral translation defaults
- **WHEN** a `mistral_translation` execution only sets `api_key`
- **THEN** it loads with endpoint `https://api.mistral.ai/v1/chat/completions`, model `mistral-small-latest`, timeout 60s, `max_input_chars` 20000, `skip_english` true, `english_min_words` 8 and `english_min_ratio` 0.25

#### Scenario: Mistral translation rejected settings
- **WHEN** a `mistral_translation` execution sets `method`, `handler` or `poll_interval`, has no `api_key`, has `input.bindings`, or sets `max_input_chars` to 0 or a negative number
- **THEN** loading fails with an error naming the file and analyzer id


## ADDED Requirements

### Requirement: Mistral translation English skip settings
The `mistral_translation` execution SHALL accept `skip_english` (boolean, default `true`), `english_min_words` (integer >= 1, default `8`) and `english_min_ratio` (number > 0 and <= 1, default `0.25`). Invalid values SHALL fail loading with an error naming the file and analyzer id. These keys SHALL be rejected for every other adapter and are not subject to `${VAR}` expansion. Registry files without them SHALL load unchanged with the defaults.

#### Scenario: Invalid English skip settings
- **WHEN** `english_min_words` is below 1 or `english_min_ratio` is 0, negative or above 1
- **THEN** loading fails with an error naming the file and analyzer id

#### Scenario: English skip settings on another adapter
- **WHEN** an adapter other than `mistral_translation` sets `skip_english`, `english_min_words` or `english_min_ratio`
- **THEN** loading fails with an error naming the file and analyzer id
