## MODIFIED Requirements

### Requirement: Container image
The repository SHALL contain a multi-stage `Dockerfile` that builds a static binary into a non-root image exposing port 8080, with a sample analyzers directory at `/etc/orchestrator/analyzers` containing only the `text_stats` analyzer and the base finalizer prompt `config/finalizer/prompt.md` (the parent issue's base prompt verbatim) at `/etc/orchestrator/finalizer/prompt.md`.

#### Scenario: Image runs
- **WHEN** the image is started without configuration
- **THEN** the server listens on 8080 and `GET /api/v1/analyzers` lists only `text_stats`

#### Scenario: Prompt in the image
- **WHEN** an `llm` finalizer omits `prompt_file` in the image
- **THEN** the prompt is read from `/etc/orchestrator/finalizer/prompt.md`

## ADDED Requirements

### Requirement: Analyzer listing hides LLM settings
`GET /api/v1/analyzers` items SHALL NOT contain `base_url`, `api_key`, `model`, `headers`, `extra_body`, `prompt_file` or the `llm` block of any analyzer (shared `analysis-jobs-api` "Registry listing hides execution settings").

#### Scenario: LLM finalizer listed
- **WHEN** the docs examples are loaded with a secret-looking API key
- **THEN** the analyzers response contains neither the key, the base URL, the model nor the `llm` description
