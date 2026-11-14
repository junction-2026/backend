## ADDED Requirements

### Requirement: wikipedia qualification and skip
The `wikipedia` adapter SHALL search only claims of the bound `topic_extraction` result with `check_worthiness` at least `min_check_worthiness`, at most `max_claims`, in `claims` order. With no qualifying claim, including an empty or missing `claims`, it SHALL return the skip outcome `no_checkable_claims` without a request. An extraction that is not a JSON object SHALL fail with `adapter_error`.

#### Scenario: Cap
- **WHEN** claims have check_worthiness 0.9, 0.8, 0.3, 0.6 and 0.5
- **THEN** the claims with 0.9, 0.8 and 0.6 are searched, in that order

#### Scenario: Nothing to look up
- **WHEN** the only claim has check_worthiness 0.2
- **THEN** the skip outcome is returned and the stub server received zero requests

### Requirement: wikipedia wikis
Each claim SHALL be searched in `{source_language}.wikipedia.org` with `query` and in `en.wikipedia.org` with `query_en`, where `source_language` is the lower-cased value of the bound `translation` result when it is a two-letter code. Otherwise, and when it is `en`, only `en.wikipedia.org` SHALL be searched, with `query_en`.

#### Scenario: German post
- **WHEN** source_language is `de`
- **THEN** each claim has two searches, `de` with `query` then `en` with `query_en`

#### Scenario: Unknown source language
- **WHEN** the translation result has no source_language, or `deu`
- **THEN** each claim has one `en` search with `query_en`

### Requirement: wikipedia search mode
`search_mode` SHALL be `fulltext` when the claim text contains a digit `0`-`9` or no entity has a character other than digits, whitespace and punctuation, and `title` otherwise.

#### Scenario: Modes
- **WHEN** the claim text is "The pension age is 70." or the entities are `["2024"]`
- **THEN** the mode is `fulltext`; for "Merkel led the CDU." with entities `["Angela Merkel", "CDU"]` it is `title`

### Requirement: wikipedia MediaWiki requests
For each lookup the adapter SHALL send one `action=query&list=search` request with `srlimit=max_hits` and the snippet and, when there are hits, one `action=query` request for their titles returning `pageprops` (disambiguation), `revisions` (revid, timestamp) and the plain-text intro extract with redirects resolved. Disambiguation SHALL be detected only from `pageprops.disambiguation`. The wiki URL SHALL be `execution.base_url_template` with `{lang}` replaced.

#### Scenario: Request shape
- **WHEN** a lookup finds hits
- **THEN** exactly two requests are sent, the second with `prop=pageprops|revisions|extracts` and `redirects=1`

### Requirement: wikipedia User-Agent
Every Wikipedia request SHALL carry `User-Agent: JunctionOrchestrator/1.0 (<execution.user_agent>)`.

#### Scenario: Header present
- **WHEN** a run sends requests
- **THEN** every request has that header with the configured contact

### Requirement: wikipedia page selection
The adapter SHALL choose the page as the shared capability defines: no hits give `no_page`; title mode picks the first entity match (a hit that is not a disambiguation page and whose title equals an entity or `entity (qualifier)`, case-insensitively, else the top hit; fulltext mode picks the top hit; a chosen disambiguation page is replaced by the first entity match, otherwise the lookup is `ambiguous` with all page members null. Both record the query sent.

#### Scenario: Entity title preferred
- **WHEN** a title-mode claim has entities `["CDU"]` and the hits are `Christian Democratic Union of Germany` then `CDU`
- **THEN** the lookup is found with title `CDU`

#### Scenario: Ambiguous
- **WHEN** the top hit is a disambiguation page and no hit matches an entity
- **THEN** the status is `ambiguous` and title, url and passage are null

### Requirement: wikipedia found lookup members
A found lookup SHALL have the page title, `url` `https://{lang}.wikipedia.org/w/index.php?title=<Title_with_underscores>&oldid=<revision_id>` (always the real wiki host), `page_id`, `revision_id` and `revised_at` (RFC 3339 UTC).

#### Scenario: Permalink
- **WHEN** page `Retirement age` at revision 1250000000 is chosen on `en`
- **THEN** the url is `https://en.wikipedia.org/w/index.php?title=Retirement_age&oldid=1250000000`, also when `base_url_template` points to a test server

### Requirement: wikipedia passage
The passage SHALL be the plain-text lead (`lead`) in title mode and the search snippet (`snippet`) in fulltext mode, the other when the preferred one is empty, and the lookup SHALL be `no_page` when Wikipedia returned neither. A lead longer than `max_passage_chars` SHALL be cut to a leading prefix on a rune boundary. A snippet SHALL be changed only by stripping HTML tags and decoding character references.

#### Scenario: Snippet markup removed
- **WHEN** the snippet is `the retirement age to <span class="searchmatch">70</span> &amp; more`
- **THEN** the passage is `the retirement age to 70 & more` with passage_source `snippet`

### Requirement: wikipedia matched entities and numbers
`matched_entities` SHALL be the claim's entities occurring in the passage under Unicode simple case folding, in `entities` order, without duplicates. `matched_numbers` SHALL be the maximal ASCII digit runs of the claim text occurring in the passage as a digit run not adjacent to another digit, in order of first appearance, without duplicates. They SHALL be computed from the passage for the current claim, also for cached answers.

#### Scenario: Digit run inside a longer number
- **WHEN** the claim text contains `70` and the passage contains `1970` only
- **THEN** matched_numbers is `[]`

### Requirement: wikipedia cache key
The adapter SHALL keep one JSON file per key in `cache_dir`, the key being sha256 of language, normalised query and search_mode. The query SHALL be normalised by NFC, trimming, collapsing internal whitespace to one space and lower-casing.

#### Scenario: Same query, other spelling
- **WHEN** queries `Retirement  Age 70` and ` retirement age 70 ` are looked up in the same wiki and mode
- **THEN** they have the same key

### Requirement: wikipedia on-disk cache
Cache files SHALL be written atomically (temporary file in the directory, then rename). Entries older than `cache_ttl` and corrupt files SHALL count as misses. Only successful answers, including `no_page` and `ambiguous`, SHALL be stored. A new Executor on the same directory SHALL read them.

#### Scenario: Restart
- **WHEN** a second run with the same claims uses a new Executor on the same `cache_dir`
- **THEN** it sends no request, `requests_used` is 0, `cached` equals the number of lookups and every lookup has `cached: true`

#### Scenario: Corrupt file
- **WHEN** a cache file holds invalid JSON
- **THEN** the lookup is requested again and the file is replaced

### Requirement: wikipedia reuse within a run
Identical (language, normalised query, search_mode) lookups within one run SHALL be answered by the first one without a further request, count toward `cached` and carry their own `query` and matches. `requests_used` SHALL count every HTTP request of the run including retries.

#### Scenario: Same query twice
- **WHEN** two claims have the same `query_en` apart from case and spacing
- **THEN** one search and one page request are sent and `cached` is 1

### Requirement: wikipedia failures and concurrency
At most `max_concurrency` requests SHALL be in flight per run. A 429 or 5xx answer SHALL be retried once. A connection error, any other non-2xx answer after that, or an undecodable answer SHALL cancel the remaining requests and fail the run with `adapter_error` and no result, with a message naming the wiki and the HTTP status but no URL. A deadline or cancellation SHALL map to `timeout` or `cancelled`.

#### Scenario: Wikipedia down
- **WHEN** the `en` search answers 503 twice
- **THEN** the run fails with `adapter_error` mentioning `en.wikipedia.org` and `503`, with no result and no passage of the `de` page
