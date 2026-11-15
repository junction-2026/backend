## ADDED Requirements

### Requirement: Public API rate limit settings
The orchestrator SHALL read `PUBLIC_API_SUBMIT_PER_MINUTE` (positive integer, default 30) and `PUBLIC_API_GET_PER_MINUTE` (positive integer, default 600). A non-integer or a value below 1 SHALL fail startup with an error naming the variable.

#### Scenario: Defaults
- **WHEN** neither variable is set
- **THEN** the submit limit is 30 and the get limit is 600 requests per minute

#### Scenario: Invalid value
- **WHEN** `PUBLIC_API_SUBMIT_PER_MINUTE` is `0` or `1.5`
- **THEN** startup fails with an error naming `PUBLIC_API_SUBMIT_PER_MINUTE`

### Requirement: Public API token buckets
Each public rate limit SHALL be a single process-wide token bucket with capacity equal to its per-minute value and a refill of per-minute/60 tokens per second. When a request finds less than one token, `retry_after_s` SHALL be `ceil((1 - tokens) / rate)`, at least 1.

#### Scenario: Retry after an exhausted bucket
- **WHEN** the submit limit is 2 per minute and both tokens were taken at the same instant
- **THEN** the next submit gets 429 with `retry_after_s` 30, and after 30 seconds a submit is admitted

#### Scenario: Capacity bounds the burst
- **WHEN** the submit bucket has been idle for an hour with a limit of 2
- **THEN** exactly two submits are admitted before the next 429

### Requirement: Public base URL setting
The orchestrator SHALL read `PUBLIC_BASE_URL`. When set it SHALL be an absolute `http` or `https` URL, otherwise startup fails with an error naming the variable. Empty or unset means `share_url` is null.

#### Scenario: Relative base URL
- **WHEN** `PUBLIC_BASE_URL` is `veil.example.org`
- **THEN** startup fails with an error naming `PUBLIC_BASE_URL`

#### Scenario: Unset
- **WHEN** `PUBLIC_BASE_URL` is unset
- **THEN** every PublicAnalysis has `share_url: null`

### Requirement: Public handler uses the engine
The public handler SHALL create jobs only through the existing engine Create path (`engine.CreateRequest`) with `Analyzers` nil, `Finalize` true and `Private` equal to `public == false`, and SHALL read jobs only through `Service.Get`.

#### Scenario: Create request
- **WHEN** a client posts `{"post": "P", "public": false}`
- **THEN** the engine receives one CreateRequest with nil `Analyzers`, `Finalize` true and `Private` true

### Requirement: Public request logging
Request logging for the public endpoints SHALL stay method, path and status only (no body, query or headers), as for existing routes.

#### Scenario: Body and query not logged
- **WHEN** a client posts a body and query string containing a marker value
- **THEN** the request log line has the path and status and no marker

### Requirement: Chat limiter unchanged
The chat limiter behaviour (analysis-chat-api "Chat request limits") SHALL be unchanged by the public rate limits.

#### Scenario: Chat tests unchanged
- **WHEN** the chat limiter tests run
- **THEN** they pass without modification

### Requirement: Public API test coverage
Tests SHALL cover every public-analysis-api scenario, the validation precedence (for example `{"post": 5}` gives 422 and `{}` gives 400 `missing_content`), composition with blank and whitespace comments, the 100-comment limit, rate limiting with an injected clock, and the mapping of techniques (classifier maximum score across Detection `models`, llm `confidence`), bot_signal validity rules, evidence null/empty distinctions and Wikipedia found-only filtering.

#### Scenario: Suite passes
- **WHEN** `go test ./... -race` runs
- **THEN** all public API tests pass

### Requirement: Public API documentation
The repository SHALL document both public endpoints in `docs/public-api.md` with request and response examples, error codes, polling guidance and the rate-limit and `PUBLIC_BASE_URL` configuration.

#### Scenario: Integrator reads the docs
- **WHEN** an integrator opens `docs/public-api.md`
- **THEN** it finds both endpoints, every error code, polling advice and the three configuration variables
