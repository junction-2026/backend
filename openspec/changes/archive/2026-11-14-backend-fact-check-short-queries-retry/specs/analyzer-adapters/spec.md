## ADDED Requirements

### Requirement: Fact check shorter retry
The `fact_check` adapter SHALL implement the shared requirements "Shorter retry for empty searches", "Shorter retry limits", "Shorter retry query" and "Query retry member" in code, without an LLM call. The retry SHALL run after the original's answer, also when that answer came from the cache, and SHALL respect `max_concurrency`.

#### Scenario: Retry query rules
- **WHEN** a search returns 0 claims
- **THEN** the retry query is the first two non-blank trimmed `entities` joined by a space (claim-language search) or the first three whitespace words of `query_en` (`en` search), and none is sent when it is empty or equals the original `q` after trimming and collapsing whitespace, compared case-insensitively

#### Scenario: Empty German search
- **WHEN** a claim with `language: de`, `query` "EU beschließt Bargeld abschaffen 2027" and `entities` ["EU", "Bargeld", "2027"] returns 0 claims
- **THEN** one more request with `query=EU Bargeld` and `languageCode=de` is sent after it, and its Query has `retry_of` "EU beschließt Bargeld abschaffen 2027"

#### Scenario: No retry
- **WHEN** the shorter query is empty or equals the original, the search failed (429/5xx twice, undecodable answer), or the search is itself a retry returning 0 claims
- **THEN** no further request is sent for it

#### Scenario: Default bound
- **WHEN** three non-English claims are searched with defaults and every search returns 0 claims without errors
- **THEN** `requests_used` is at most 12

#### Scenario: Rejected key on a retry
- **WHEN** a retry is answered with HTTP 400
- **THEN** the analyzer fails per "Fact check rejected key" and sends no further request

### Requirement: Extractor prompt query wording
`config/extractor/prompt.md` SHALL require `query` and `query_en` of 2-4 words (main actor plus key object or action noun, infinitive or noun form) and SHALL list what to leave out: dates and years, relative time, numbers unless the number is the claim, verbs of deciding or saying, hedges and framing. `text` SHALL stay the full claim.

#### Scenario: Prompt content
- **WHEN** the prompt file is read
- **THEN** it states the 2-4 word rule and the leave-out list

### Requirement: Extractor prompt examples
`config/extractor/prompt.md` SHALL give examples of: the pension claim with query "pension age 70"; a decision and a plan with the same short query; and two posts with `claims: []`, an opinion and a forecast.

#### Scenario: Prompt examples
- **WHEN** the prompt file is read
- **THEN** it contains "Die EU hat gestern beschlossen, das Bargeld ab 2027 komplett abzuschaffen." with query "EU Bargeld abschaffen" and query_en "EU abolish cash", "Die EU will das Bargeld 2027 abschaffen" as the claim "Die EU plant, das Bargeld 2027 abzuschaffen.", and "politicians are all the same, wake up" and "The economy will collapse next year, wake up" with `claims: []`

#### Scenario: Stub LLM acceptance examples
- **WHEN** a stub LLM answers as the prompt instructs for the Bargeld decision, the Bargeld plan and the economy forecast post
- **THEN** the decision post yields one claim whose queries have at most 4 words and contain no year, "gestern", "beschließt" or "Eilmeldung", the plan post yields one claim, and the forecast post yields `claims: []`

## MODIFIED Requirements

### Requirement: Extractor prompt claims
`config/extractor/prompt.md` SHALL define claims as verifiable facts, including statements that someone decided, plans, wants, announced or will do something, and SHALL exclude opinions, insults, forecasts about uncertain outcomes nobody controls, questions, calls to action and vague accusations. It SHALL give the rules for `text`, `language`, `entities` and `check_worthiness` and say never to invent a claim.

#### Scenario: Prompt content
- **WHEN** the prompt file is read
- **THEN** it contains the rules above

### Requirement: Fact check result mapping
The adapter SHALL build the FactCheckResult per the shared requirements "FactCheckResult object", "Query order", "Query object", "Query retry member" and "FactCheck object", mapping provider `claims[]` and `claimReview[]` members to the FactCheck members (missing ones null). `requests_used` SHALL count all HTTP requests (backoff and shorter retries included) and `cached` all queries answered from the cache.

#### Scenario: Mapping
- **WHEN** the provider returns a claim with one review carrying publisher, url, title, date, rating and language
- **THEN** the FactCheck holds `claim`, `claimant`, `claim_date`, `publisher`, `site`, `url`, `title`, `rating`, `language`, `review_date`, `for_claim` and `query`

#### Scenario: Retry placement and accounting
- **WHEN** A/de returns 0 claims and its retry returns 5
- **THEN** `queries` lists A/de, A/de retry (with `retry_of` set, planned searches `retry_of: null`), A/en, `requests_used` counts the retry, and the retry's reviews are in `fact_checks` with `query` = the retry `q`

### Requirement: Fact check list assembly
The adapter SHALL assemble `fact_checks` per the shared requirement "Fact check list assembly": drop reviews without `url` and repeated `url`s (first in planning order kept, a retry planned directly after its original), sort by parseable `review_date` descending (unparseable last, ties stable) and cap at `max_results` (default 10).

#### Scenario: Duplicates and order
- **WHEN** the German and English query of one claim return the review `https://correctiv.org/x`, plus a review without url and an older review
- **THEN** `fact_checks` holds the review once with the German `query`, then the older one, and `queries` lists the German then the English search

#### Scenario: Cap
- **WHEN** 14 distinct reviews are returned and `max_results` is 10
- **THEN** `fact_checks` holds the 10 newest, newest first

### Requirement: Fact check cache
The adapter SHALL keep an in-memory cache of successful provider answers keyed by `(query, languageCode)` for `cache_ttl` (default 24h), serve a hit without a request (Query `cached: true`), not cache failures and persist nothing. Shorter retries are cached and served like any search.

#### Scenario: Repeated job
- **WHEN** an identical job runs within `cache_ttl`
- **THEN** the stub receives no request, `requests_used` is 0, `cached` equals the number of queries (retries included) and every Query has `cached: true`

#### Scenario: Failure not cached
- **WHEN** a query fails and the same job runs again
- **THEN** the second job sends a request for it again
