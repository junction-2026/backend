## MODIFIED Requirements

### Requirement: Orchestrator fixed text translations
The localization SHALL hold reviewed fixed translations of the orchestrator's insufficient-evidence `user_text` (`finalizer.InsufficientText`) for every supported language: `en` (the text itself), `fr`, `ar` (Modern Standard Arabic), `de`, `fi` and `ru`, and SHALL use them for a final assessment with `produced_by: orchestrator` without a provider call.

#### Scenario: Arabic
- **WHEN** a job finalized by the orchestrator is localized into `ar`
- **THEN** `user_text` is `translated` with `source_language: "en"` and the Arabic fixed text, and no request is sent

#### Scenario: Russian
- **WHEN** a job finalized by the orchestrator is localized into `ru`
- **THEN** `user_text` is `translated` with `source_language: "en"` and the Russian fixed text, and no request is sent
