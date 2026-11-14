## ADDED Requirements

### Requirement: Topic extraction claims schema
The `llm` adapter with task `topic_extraction` SHALL include in its built-in JSON schema a required `claims` array of objects with the required members `text`, `language`, `query`, `query_en`, `entities` (array of strings) and `check_worthiness` (number), and SHALL send that schema when `response_format` is `json_schema` (and in the user message in the other modes).

#### Scenario: Schema sent
- **WHEN** the analyzer runs with `response_format: json_schema`
- **THEN** the request's schema requires `topics`, `news` and `claims`, and each claim requires the six members

### Requirement: Topic extraction claims validation and normalization
The adapter SHALL validate `claims` per the shared requirement "Claim validation": a reply without a `claims` array, or with an invalid claim, fails with `invalid_output`. It SHALL normalize per "Claim normalization": absent or null `entities` become `[]`, `check_worthiness` is clamped to 0..1, claims are ordered by it descending (ties in the model's order) and capped at `execution.max_claims` (default 5), other members are dropped.

#### Scenario: Claim of the pension example
- **WHEN** the content is "The government raised the pension age to 70 last year, they are robbing us!" and the stub reply has one claim with `check_worthiness` 0.8
- **THEN** the result has one Claim with `text`, `language`, `query`, `query_en`, `entities` and `check_worthiness` 0.8

#### Scenario: Ordered, clamped and capped
- **WHEN** `max_claims` is 2 and the reply has claims with `check_worthiness` 0.3, 1.4 and 0.6
- **THEN** the result has claims with `check_worthiness` 1 and 0.6, in that order

#### Scenario: Reply without claims
- **WHEN** the reply is `{"topics":[],"news":[]}` or a claim lacks `check_worthiness`
- **THEN** the analyzer fails with `invalid_output` and `result: null`

#### Scenario: Opinion only
- **WHEN** the reply has `"claims": []`
- **THEN** the analyzer succeeds with `claims: []`

### Requirement: Extractor prompt claims
`config/extractor/prompt.md` SHALL define what counts as a claim (verifiable facts: events, numbers, quotes, causal claims, attributions; not opinions, insults, predictions, questions, calls to action or vague accusations) and the rules for `text`, `language`, `query` (3-8 words), `query_en`, `entities` and `check_worthiness`. It SHALL say never to invent a claim and give two short examples: one checkable claim, and an opinion-only post with `claims: []`.

#### Scenario: Prompt content
- **WHEN** the prompt file is read
- **THEN** it contains the rules above and both examples

### Requirement: Fact check request
The `fact_check` adapter SHALL read only the binding `extraction` (the `topic_extraction` result) and its `claims`, plan searches per the shared requirements "Qualifying claims" and "Search queries per claim", and send for each planned search `GET {base_url}/claims:search?query=<q>&languageCode=<l>&pageSize=<page_size>&maxAgeDays=<max_age_days>&key=<api_key>` with at most `max_concurrency` (default 4) requests in flight. It SHALL NOT call an LLM or a translation service.

#### Scenario: German claims
- **WHEN** two qualifying claims have `language: de`
- **THEN** the stub receives at most 4 requests: per claim `query` with `languageCode=de` and `query_en` with `languageCode=en`, each with `pageSize`, `maxAgeDays` and `key`

#### Scenario: English claim and duplicates
- **WHEN** a qualifying claim has `language: en-GB`, and a second claim has the same `query` and `languageCode`
- **THEN** one request is sent for the pair

#### Scenario: Threshold and cap
- **WHEN** claims have `check_worthiness` 0.7, 0.2, 0.9, 0.8 and 0.6 and defaults apply
- **THEN** the claims 0.7, 0.9 and 0.8 are searched in that order and no others

#### Scenario: Concurrency bound
- **WHEN** four queries are planned and `max_concurrency` is 2
- **THEN** the stub never sees more than 2 requests in flight

### Requirement: Fact check result mapping
The adapter SHALL build the FactCheckResult per the shared requirements "FactCheckResult object", "Query order", "Query object" and "FactCheck object": it maps the provider's `claims[]` and `claimReview[]` members to the FactCheck members (missing ones null) and counts `requests_used` (HTTP requests sent, retries included) and `cached` (queries answered from the cache).

#### Scenario: Mapping
- **WHEN** the provider returns a claim with one review carrying publisher, url, title, date, rating and language
- **THEN** the FactCheck holds `claim`, `claimant`, `claim_date`, `publisher`, `site`, `url`, `title`, `rating`, `language`, `review_date`, `for_claim` and `query`

### Requirement: Fact check list assembly
The adapter SHALL assemble `fact_checks` per the shared requirement "Fact check list assembly": drop reviews without `url` and repeated `url`s (first in planning order kept), sort by parseable `review_date` descending (unparseable last, ties stable) and cap at `max_results` (default 10).

#### Scenario: Duplicates and order
- **WHEN** the German and English query of one claim return the review `https://correctiv.org/x`, plus a review without url and an older review
- **THEN** `fact_checks` holds the review once with the German `query`, then the older one, and `queries` lists the German then the English search

#### Scenario: Cap
- **WHEN** 14 distinct reviews are returned and `max_results` is 10
- **THEN** `fact_checks` holds the 10 newest, newest first

### Requirement: Fact check rating table
The adapter SHALL derive `rating_normalized` from `textualRating` by a built-in table of common English and major European terms, matched case-insensitively after trimming whitespace and trailing punctuation, without an LLM call. The table SHALL map at least False, Falsch, Faux, Falso to `false`; Misleading, Irreführend, Trompeur to `misleading`; Partly true, Teilweise richtig, Mixture to `mixed`; True, Richtig, Vrai to `true`. Other or missing ratings are `unrated`; `rating` is kept verbatim.

#### Scenario: Terms
- **WHEN** ratings are `Faux`, `Irreführend`, `Mixture`, ` FALSCH. ` and `Fehlender Kontext`
- **THEN** `rating_normalized` is `false`, `misleading`, `mixed`, `false` and `unrated`, and `rating` is unchanged

### Requirement: Fact check cache
The adapter SHALL keep an in-memory cache of successful provider answers keyed by `(query, languageCode)` for `cache_ttl` (default 24h), serve a hit without a request (Query `cached: true`), not cache failures and persist nothing.

#### Scenario: Repeated job
- **WHEN** an identical job runs within `cache_ttl`
- **THEN** the stub receives no request, `requests_used` is 0, `cached` equals the number of queries and every Query has `cached: true`

#### Scenario: Failure not cached
- **WHEN** a query fails and the same job runs again
- **THEN** the second job sends a request for it again

### Requirement: Fact check failures
The adapter SHALL retry a 429 or 5xx answer once after a backoff (a 429's `Retry-After` seconds, capped at 5s). A query still failing, or with an undecodable answer or another non-2xx status, gets `status: failed`, `claims: 0` and `error` `{code: search_failed, message}`. The analyzer fails with `adapter_error` only when every query failed or the `topic_extraction` result is not a JSON object; timeout and cancellation map to `timeout` and `cancelled`.

#### Scenario: Partial failure
- **WHEN** one of three queries answers 429 twice and the others succeed
- **THEN** the analyzer succeeds, that Query is `failed` with `search_failed`, and `requests_used` counts both attempts

#### Scenario: Every query fails
- **WHEN** every query answers 503 twice
- **THEN** the analyzer fails with `adapter_error`

### Requirement: Fact check rejected key
On HTTP 400, 401 or 403 the adapter SHALL fail with `adapter_error`, send no request not yet sent, and say in the message that the api_key is invalid or the Fact Check Tools API is not enabled for the key's Google Cloud project.

#### Scenario: API not enabled
- **WHEN** the first answer is HTTP 403
- **THEN** the analyzer fails with `adapter_error`, the message names the api_key or the API not being enabled, and no later query is sent

### Requirement: Fact check key redaction
The API key SHALL be sent only as the `key` query parameter and SHALL NOT appear in any error message, log line or result string, also when the provider echoes the key or the request URL in its body.

#### Scenario: Provider echoes the URL
- **WHEN** the provider answers 400 with a message containing the request URL and `key=<api key>`
- **THEN** the error message contains neither the key nor its URL-encoded form

### Requirement: Fact check skip
When no claim qualifies, including a missing or empty `claims` member, the adapter SHALL send no request and return the skip reason `no_checkable_claims`.

#### Scenario: Opinion-only and older extractor
- **WHEN** `claims` is `[]` or the extraction has no `claims` member
- **THEN** the run is skipped with `no_checkable_claims` and the stub received zero requests
