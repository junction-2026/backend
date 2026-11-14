## MODIFIED Requirements

### Requirement: Web page configuration
The orchestrator SHALL read the Oxylabs settings `OXYLABS_API`, `OXYLABS_WEB_API_KEY`, `OXYLABS_USERNAME`, `OXYLABS_PASSWORD`, `OXYLABS_AI_STUDIO_API_KEY`, `OXYLABS_AI_STUDIO_URL`, `OXYLABS_LOCATION` and the web settings `WEB_MIN_TEXT_CHARS`, `WEB_CACHE_TTL`, `WEB_TIMEOUT_SECONDS` from the environment, with the rules of the scenarios. An invalid value SHALL abort startup with a message naming the variable, never its value; missing credentials SHALL NOT abort startup.

#### Scenario: Defaults
- **WHEN** none of the variables is set
- **THEN** the API is `web_api`, the minimum text length 300, the cache lifetime 1 hour and the rendered-fetch timeout 150 seconds

#### Scenario: Credentials per API
- **WHEN** a web page is scraped
- **THEN** `web_api` needs `OXYLABS_WEB_API_KEY`, `web_scraper_api` needs `OXYLABS_USERNAME` and `OXYLABS_PASSWORD`, `ai_studio` needs `OXYLABS_AI_STUDIO_API_KEY`, and a missing one fails the scrape with `platform_not_configured` naming it

#### Scenario: Value types
- **WHEN** the variables are set
- **THEN** `OXYLABS_LOCATION` is a two-letter country code, `WEB_MIN_TEXT_CHARS` a positive integer, `WEB_CACHE_TTL` a positive Go duration and `WEB_TIMEOUT_SECONDS` a positive number

#### Scenario: Invalid location
- **WHEN** `OXYLABS_LOCATION` is `DEU`
- **THEN** startup fails with a message naming `OXYLABS_LOCATION` and not `DEU`

#### Scenario: Invalid selector
- **WHEN** `OXYLABS_API` is `other`
- **THEN** startup fails with a message naming `OXYLABS_API`

#### Scenario: Timeout default follows the upstream timeout
- **WHEN** `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` is 200 and `WEB_TIMEOUT_SECONDS` is unset
- **THEN** the rendered-fetch timeout is 200 seconds

#### Scenario: AI Studio selector
- **WHEN** `OXYLABS_API` is `ai_studio` and `OXYLABS_AI_STUDIO_API_KEY` is not set
- **THEN** startup succeeds and a web scrape fails with `platform_not_configured` naming `OXYLABS_AI_STUDIO_API_KEY`

#### Scenario: AI Studio URL
- **WHEN** `OXYLABS_AI_STUDIO_URL` is not set, or is an absolute http(s) URL, or is `api-aistudio.oxylabs.io`
- **THEN** the base URL is `https://api-aistudio.oxylabs.io`, that URL, or startup fails with a message naming `OXYLABS_AI_STUDIO_URL` and not its value

## ADDED Requirements

### Requirement: AI Studio request headers
With `OXYLABS_API=ai_studio` every request to AI Studio SHALL carry `x-api-key: <OXYLABS_AI_STUDIO_API_KEY>`, `Content-Type: application/json` and `User-Agent: junction-backend/<version>`, and no `Authorization` header.

#### Scenario: Headers
- **WHEN** the stub receives a run request
- **THEN** it has the `x-api-key` header with the key, a `junction-backend/` User-Agent and no `Authorization` header

### Requirement: AI Studio run lifecycle
A run SHALL be `POST {base}/scrape` with a JSON body `url`, `output_format`, `render_javascript: true` and, for `json`, `openapi_schema`. The answer's `run_id` SHALL be polled with `GET {base}/scrape/run?run_id=<id>`, first after 1 second and then backing off to at most 3 seconds, until the status is `completed` or a failure status; its result SHALL then be read with `GET {base}/scrape/run/data?run_id=<id>`. The poll interval SHALL be injectable for tests.

#### Scenario: JSON run
- **WHEN** the stub answers a run id, one `processing` poll, `completed` and a data object
- **THEN** the stub saw `output_format: "json"`, `render_javascript: true` and an `openapi_schema` with a property `body`

### Requirement: AI Studio run bounds
Each run SHALL end after `WEB_TIMEOUT_SECONDS`, failing with `upstream_timeout`, and all requests SHALL use the job context and stop when it is cancelled.

#### Scenario: Run timeout
- **WHEN** a run is still `processing` after `WEB_TIMEOUT_SECONDS` and the Markdown run also times out
- **THEN** the scrape fails with `upstream_timeout`

#### Scenario: Cancellation
- **WHEN** the job context is cancelled while a run is polled
- **THEN** polling stops promptly and no further run is started

### Requirement: AI Studio run schema
The JSON run SHALL send an object schema with the string properties `title`, `site_name`, `author`, `published_at` (description "ISO 8601 publication date-time, empty if not shown"), `lang` (description "ISO 639-1 code of the article's language"), `canonical_url`, `description`, `body` (the main text of the page without navigation, advertising, cookie banners and comments) and `page_kind` (enum `article`, `post`, `other_content`, `login_wall`, `error_page`, `consent_or_paywall`).

#### Scenario: Schema members
- **WHEN** the JSON run is started
- **THEN** its `openapi_schema` has all nine properties and `page_kind` has the six enum values

### Requirement: AI Studio output is untrusted
Each member of the answer SHALL be type-checked and a non-string ignored. Control characters except `\n` and `\t` and the zero-width characters U+200B, U+200C, U+200D, U+2060 and U+FEFF SHALL be removed; metadata strings SHALL be trimmed and capped at 500 characters, and an empty string becomes null.

#### Scenario: Hostile output
- **WHEN** the body contains U+200B, U+FEFF and NUL characters, `author` is a number and `title` is 600 characters long
- **THEN** the text has none of those characters, `author` is null and `title` has 500 characters

### Requirement: AI Studio result mapping
`text` SHALL be the sanitized `body` capped like other page texts (20,000 characters, `truncated`). `published_at` SHALL be parsed as RFC 3339/ISO 8601 into UTC and be null when unparseable; `lang` SHALL be lower-cased and kept only when it matches `^[a-z]{2}$`; `canonical_url` SHALL be kept only when an absolute http(s) URL of at most 500 characters. `host` and `domain` SHALL be computed from `final_url`, the requested URL, and `word_count` from `text`.

#### Scenario: Invalid metadata
- **WHEN** `published_at` is `yesterday` and `lang` is `eng`
- **THEN** both are null

### Requirement: AI Studio page kinds
`login_wall` SHALL fail with `login_required`, `error_page` with `no_text_content`, and `consent_or_paywall` with a body shorter than `WEB_MIN_TEXT_CHARS` with `login_required` and a message naming consent or paywall. A missing or unknown `page_kind` SHALL be treated as `other_content`.

#### Scenario: Unknown page kind
- **WHEN** the JSON run answers a long body and `page_kind` `weird`
- **THEN** the scrape succeeds with `extraction: "ai_studio_json"`

#### Scenario: Short paywall
- **WHEN** the page kind is `consent_or_paywall` and the body has 100 characters
- **THEN** the scrape fails with `login_required` and a consent or paywall message

### Requirement: AI Studio Markdown fallback run
When the JSON run fails (not with HTTP 401/403), or its body is shorter than `WEB_MIN_TEXT_CHARS` for a page of kind article, post or other content, the module SHALL start one run with `output_format: "markdown"`, `render_javascript: true` and no schema, and use its text through the Markdown-to-text function with `extraction: "markdown_fallback"`. Metadata of a succeeded JSON run SHALL be kept.

#### Scenario: Fallback
- **WHEN** the JSON run returns a 50-character body of kind article and the Markdown run a long text
- **THEN** `extraction` is `markdown_fallback`, `oxylabs_requests` 2 and the JSON run's title is kept

### Requirement: AI Studio fallback outcome and budget
When the Markdown text is not longer than a short JSON body, the JSON body SHALL be used; without any text the scrape SHALL fail with `no_text_content`; when both runs failed it SHALL fail with the error of the Markdown run. A scrape SHALL never start more than 2 runs and `oxylabs_requests` SHALL count the runs started.

#### Scenario: Run budget
- **WHEN** both runs end in a failure status
- **THEN** exactly 2 runs were started and the scrape fails

### Requirement: AI Studio errors
A non-2xx answer to the start, a poll or the data request, or a failure status, SHALL end the run as failed with `upstream_error` and, when the answer has an `error_code` that is a short token not containing the API key, `details.provider_error_code`. An HTTP 401 or 403 SHALL fail the scrape with `upstream_error` at once, without a Markdown run.

#### Scenario: Failed run
- **WHEN** both runs end with status `failed` and `error_code` `target_unreachable`
- **THEN** the scrape fails with `upstream_error` and `details.provider_error_code` `target_unreachable`

#### Scenario: Rejected key
- **WHEN** AI Studio answers HTTP 401 to the first run
- **THEN** the scrape fails with `upstream_error` and no second run is started

### Requirement: AI Studio credential handling
The API key and request headers SHALL NOT appear in logs, errors, details or the job output; only run ids, statuses and durations may be logged.

#### Scenario: Echoed key
- **WHEN** a stub echoes the API key in its error answers
- **THEN** the key appears in no error, detail or result of the module

### Requirement: AI Studio caching
A successful AI Studio result SHALL be stored in the page cache under the existing key rules; a repeated scrape within `WEB_CACHE_TTL` SHALL send no request (`cached: true`, `oxylabs_requests: 0`) and failures SHALL NOT be cached.

#### Scenario: Second scrape
- **WHEN** the same URL is scraped twice within the cache lifetime
- **THEN** the second scrape sends no request and has `cached: true` and `oxylabs_requests: 0`

### Requirement: AI Studio tests and documentation
Tests SHALL use an AI Studio stub only and never reach the real service. The README settings table SHALL document `OXYLABS_API=ai_studio`, `OXYLABS_AI_STUDIO_API_KEY` and `OXYLABS_AI_STUDIO_URL`.

#### Scenario: Offline tests
- **WHEN** the web tests run without network access
- **THEN** every AI Studio request goes to the stub
