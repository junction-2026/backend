## ADDED Requirements

### Requirement: news_search queries and languages
The `news_search` adapter SHALL search the `query` of the first `max_topics` Topics (origin `topic`), then the `headline` of the first `max_news` NewsItems (origin `news`), once per language. Originals are assumed to be in the source language; a query needs translation unless its language equals the known source language.

#### Scenario: Two topics in German and English
- **WHEN** two topics, no news, source `de`, languages `[source, en]`
- **THEN** four searches are sent (`de` with the original, `en` with the translation) and the result has languages `[de, en]` and four SearchQuery entries

### Requirement: news_search language resolution
The language `source` SHALL resolve to the lower-cased `source_language` of the bound `translation` result and SHALL be dropped when there is no binding or the value is null or not a two-letter code; duplicates SHALL be removed keeping the first.

#### Scenario: Unknown source
- **WHEN** the translation result has `source_language: null` and languages are `[source, en]`
- **THEN** `source` is dropped and the `en` queries are translated

### Requirement: news_search nothing to search
When the topics input has no Topic and no NewsItem the adapter SHALL return the skip outcome `no_searchable_topics` without sending a search or translation request. A topics input that is not a TopicExtraction object SHALL fail with `adapter_error`.

#### Scenario: Empty extraction
- **WHEN** topics and news are empty
- **THEN** the skip outcome is returned and the stub servers received zero requests

### Requirement: news_search query translation
For each target language with queries needing translation the adapter SHALL send one Mistral chat completions request (`Authorization: Bearer <translate.api_key>`) whose user message is a JSON array of those queries and expect a JSON array of the same length of non-empty strings. A failed request or a malformed reply SHALL mark those entries `failed` with `error.code: translation_failed` and no search SHALL be sent for them.

#### Scenario: One request per language
- **WHEN** three queries need English
- **THEN** exactly one Mistral request carries all three

#### Scenario: Translation failure
- **WHEN** Mistral answers 500 twice
- **THEN** the English entries are `failed` with `translation_failed`, no English search is sent and the source-language entries succeed

#### Scenario: Rejected translation key
- **WHEN** Mistral answers 401
- **THEN** the run fails with `adapter_error` naming `execution.translate.api_key`

### Requirement: news_search provider requests
The adapter SHALL `POST {base_url}/news` with `{q, hl, gl, num}` and the header `X-API-KEY`, where `hl` is the language and `gl` the country from the table en→us, de→de, fr→fr, else the language code. When the news search returns fewer than `min_results` results it SHALL send one `POST {base_url}/search` for that query and language only.

#### Scenario: Fallback
- **WHEN** one news search returns 1 result with `min_results` 2
- **THEN** exactly one `/search` request is sent for that query and language, its entry has `endpoint: search` and `results` counting both, and the other entries stay `news`

### Requirement: news_search retries and aborts
The adapter SHALL keep at most `max_concurrency` provider requests in flight per run and retry HTTP 429 and 5xx once after a backoff of 1s (honouring context cancellation), counting both attempts in `searches_used`. On HTTP 401/403 it SHALL cancel the remaining searches and fail the run with `adapter_error` naming `execution.api_key` (`execution.translate.api_key` for Mistral).

#### Scenario: Retry
- **WHEN** a search answers 429 then 200
- **THEN** the entry succeeds and `searches_used` counts both requests

#### Scenario: Rejected key
- **WHEN** Serper answers 401
- **THEN** the run fails with `adapter_error` naming `execution.api_key` and no further searches start

#### Scenario: Partial and total failure
- **WHEN** one entry fails with 503 twice and others succeed
- **THEN** the run succeeds listing that entry `failed` with `search_failed`; when every entry fails the run fails with `adapter_error`

### Requirement: news_search cache
The adapter SHALL keep an in-memory cache per process of successful provider responses keyed by (endpoint, q, hl, gl) for `cache_ttl`; identical concurrent requests SHALL share one provider request; failed responses SHALL NOT be cached. Cache hits and shared requests SHALL count in `cached` and not in `searches_used`.

#### Scenario: Second job
- **WHEN** an identical job runs within `cache_ttl`
- **THEN** zero provider requests are sent, `searches_used` is 0 and `cached` is greater than 0

### Requirement: news_search merging
The adapter SHALL build `articles` from the succeeded entries: only `http`/`https` URLs, deduplicated by URL with lower-cased scheme and host and no fragment, `domain` the lower-cased host without a leading `www.`, ordered by provider rank ascending, then entries with a parseable date first and newest first, then SearchQuery order; then at most `per_domain` per domain and at most `max_results` in total.

#### Scenario: Duplicates and limits
- **WHEN** searches return `https://www.example.com/a#top` and `https://WWW.EXAMPLE.COM/a`, and five distinct `example.org` articles with `per_domain` 2
- **THEN** one Article has the first URL and two Articles have `domain: example.org`

### Requirement: news_search hides credentials
Every error message, SearchQuery error and log line of the adapter SHALL have the search API key, the translation API key and `X-API-KEY`/`Authorization` header values replaced by a placeholder; keys SHALL NOT be logged.

#### Scenario: Provider echoes the key
- **WHEN** Serper answers 500 with a body that repeats the API key
- **THEN** the entry error message, the result and the run error do not contain the key
