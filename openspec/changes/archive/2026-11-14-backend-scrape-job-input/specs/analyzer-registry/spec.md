## ADDED Requirements

### Requirement: Bot detection example
`docs/examples/bot_detection.yaml` SHALL define the analyzer `bot_detection` (category `bot_detection`, `enabled: false`, adapter `http_sync`, endpoint `${BOT_DETECTION_URL}`, the bot detector service on port 8001) with `input.bindings` `{content: "$input", source: "$input.source"}`, so it receives the scraped profile, history and comments in `source.metadata.scrape`. The README SHALL document it, the scraper variables and creating a job from a URL with a curl example.

#### Scenario: Example loads disabled
- **WHEN** the example is loaded without `BOT_DETECTION_URL` set
- **THEN** loading succeeds and `bot_detection` is disabled with the two bindings
