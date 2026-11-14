# web-page-scraper Specification

## Purpose
Backend-local behaviour of the `web` scrape module that fetches any http(s) page through Oxylabs: configuration, provider request builders, text and metadata extraction, caching, timeouts, credential handling, tests and documentation. The shared contract is `social-scraper-api` and `analysis-jobs-api` (change `web-page-scraping`).

## Requirements

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

### Requirement: Web API request
With `OXYLABS_API=web_api` each provider request SHALL be `POST https://webapi.oxylabs.io/v1/scrape` with `Authorization: Bearer <OXYLABS_WEB_API_KEY>`. The endpoint SHALL be overridable on the module for tests. The JSON body has `url`, `output` `["html","markdown"]`, `run_js`, `device` `"desktop"` and `location` when set.

#### Scenario: Unrendered request
- **WHEN** a page is fetched the first time
- **THEN** the body has `run_js: false`, `output` `["html","markdown"]`, `device: "desktop"`, the requested URL without fragment and no `location`

#### Scenario: Location
- **WHEN** `OXYLABS_LOCATION` is `us`
- **THEN** the body has `location: "US"`

#### Scenario: Answer members
- **WHEN** the provider answers with `results[0]`
- **THEN** the module reads `html`, `markdown` and, when present, the final URL (`url`) and the target status (`status_code`)

### Requirement: Web Scraper API request
With `OXYLABS_API=web_scraper_api` each provider request SHALL be `POST https://realtime.oxylabs.io/v1/queries` with HTTP Basic authentication and a JSON body `{"source": "universal", "url", "markdown": true}` plus `"render": "html"` only on the rendered fetch and `"geo_location"` when `OXYLABS_LOCATION` is set. The answer `results[0]` SHALL be mapped to the same internal page result: `content` that is an HTML document is the HTML, other `content` is the Markdown.

#### Scenario: Rendered request
- **WHEN** the unrendered answer has too little text
- **THEN** the second request has `render: "html"` and the first has no `render`

#### Scenario: Markdown content
- **WHEN** the answer's `content` is Markdown
- **THEN** the scrape succeeds with `extraction: "markdown_fallback"`

### Requirement: Main text extraction
The module SHALL extract the main text of the returned HTML with `github.com/markusmobius/go-trafilatura`, comments excluded, one paragraph or heading per line, whitespace collapsed, lines joined by `\n`. Text over 20,000 characters SHALL be cut at the last whitespace at or before character 20,000 and `truncated` SHALL be true.

#### Scenario: Article without navigation
- **WHEN** the recorded news article is extracted
- **THEN** the text holds the article paragraphs on separate lines and neither the navigation, the footer, the related links nor the reader comments

#### Scenario: No whitespace boundary
- **WHEN** the main content is 25,000 characters without whitespace (for example CJK text)
- **THEN** the text is cut at exactly 20,000 characters, is not empty and `truncated` is true

#### Scenario: Cut at a word boundary
- **WHEN** the main content has 37,000 characters
- **THEN** the text has at most 20,000 characters, ends with a complete word and `truncated` is true

### Requirement: Page metadata precedence
Metadata SHALL be read from JSON-LD (top-level, array and `@graph` objects), Open Graph and HTML meta tags, the first non-empty source winning in the order of scenario "Order of sources". Absent values SHALL be null; `published_at` SHALL be parsed to UTC and be null when unparseable.

#### Scenario: Order of sources
- **WHEN** a page offers several sources for a member
- **THEN** the winner follows this order, per member:
  - `title`: JSON-LD `headline`, `og:title`, `<title>`
  - `site_name`: `og:site_name`, JSON-LD `publisher.name`
  - `author`: JSON-LD `author.name`, `meta name=author`, `article:author` (not a URL)
  - `published_at`: JSON-LD `datePublished`, `article:published_time`
  - `lang`: `<html lang>`, `og:locale` with `_` as `-`
  - `canonical_url`: `<link rel=canonical>`, `og:url`, absolute http(s) only
  - `description`: `og:description`, `meta name=description`

#### Scenario: JSON-LD wins
- **WHEN** a page has JSON-LD `headline` `Election results` and `og:title` `Other`
- **THEN** `title` is `Election results`

#### Scenario: Fallbacks
- **WHEN** a page has only `<title>`, `meta name=author` and `og:locale` `de_DE`
- **THEN** `title` is the `<title>` text, `author` the meta author and `lang` `de-DE`

#### Scenario: Relative canonical ignored
- **WHEN** `<link rel=canonical href="/a">` is the only canonical hint
- **THEN** `canonical_url` is null

### Requirement: Host and domain
`host` SHALL be the lower-case host of the final URL without a leading `www.` and trailing dot; `domain` SHALL be its registered domain from `golang.org/x/net/publicsuffix` `EffectiveTLDPlusOne`, null for an IP host.

#### Scenario: Multi-label suffix
- **WHEN** the final URL is `https://news.bbc.co.uk/a`
- **THEN** `host` is `news.bbc.co.uk` and `domain` is `bbc.co.uk`

#### Scenario: IP host
- **WHEN** the final URL is `https://8.8.8.8/page`
- **THEN** `domain` is null

### Requirement: Markdown fallback text
When the extracted HTML text is shorter than the minimum text length after the last fetch and the provider's Markdown has more text, the module SHALL use that Markdown reduced to plain text: headings, emphasis, list markers, quotes, code fences, images and link targets removed (link labels kept), wrapped lines of one paragraph joined, paragraphs joined by `\n`.

#### Scenario: Markdown only
- **WHEN** the HTML has no main content and the Markdown has headings, bold text, a list, an image and a link
- **THEN** `text` has none of `#`, `**`, `![`, `](` or the link target and keeps the words and the link label

### Requirement: Login-wall detector
After the rendered fetch, when the best text is shorter than the minimum text length, the module SHALL fail with `login_required` if the page has an `<input type="password">`, a `<form>` whose action, id, class or text indicates log in or sign in, or a final URL path equal to or below `/login`, `/signin` or `/accounts/login`. A page with enough text SHALL never be a login wall.

#### Scenario: Password field
- **WHEN** the rendered page has a password input and 20 characters of text
- **THEN** the scrape fails with `login_required` and `details.platform` `web`

#### Scenario: Long page with a login form
- **WHEN** an article of 1,500 characters contains a newsletter login form
- **THEN** the scrape succeeds

### Requirement: Rendering, retries and request budget
The module SHALL send an unrendered request first and a rendered one only when the extracted text is shorter than the minimum text length. A provider answer 429 or 5xx SHALL be retried at most once per scrape (any request), so a scrape sends at most 3 provider requests. An unrendered request SHALL be bounded by `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`, a rendered one by `WEB_TIMEOUT_SECONDS`; redirects of the provider are not followed.

#### Scenario: Budget spent
- **WHEN** the unrendered request answers 503, the retry returns a JavaScript shell and the rendered request answers 503
- **THEN** the scrape fails with `upstream_error` after exactly 3 provider requests

#### Scenario: Retry succeeds
- **WHEN** the first request answers 429 and the second returns the article
- **THEN** the scrape succeeds with `oxylabs_requests: 2` and `rendered: false`

#### Scenario: Throttled twice
- **WHEN** both requests answer 429 with `Retry-After: 17`
- **THEN** the scrape fails with `rate_limited` and `details.retry_after_s` 17

### Requirement: Page cache keys
The cache key SHALL be the requested URL with lower-case scheme and host, no fragment, no trailing dot of the host, the default port removed and the query kept. The cache SHALL hold at most 500 results and evict expired entries first, then the oldest. A cached result SHALL be returned as a copy with `cached: true`, `oxylabs_requests: 0`; the stored result SHALL NOT change.

#### Scenario: Equivalent spellings
- **WHEN** `https://www.dw.com/a` is scraped and then `HTTPS://WWW.DW.COM:443/a#x`
- **THEN** the second scrape is served from the cache

#### Scenario: Different query
- **WHEN** `https://www.dw.com/a?page=2` is scraped after `https://www.dw.com/a`
- **THEN** a provider request is sent

#### Scenario: Eviction
- **WHEN** a 501st result is stored
- **THEN** the oldest entry is gone and the cache holds 500 entries

### Requirement: Job scrape timeout default
The default job-level `SCRAPER_TIMEOUT` SHALL be 360 seconds, enough for an unrendered fetch, a rendered fetch of at least 150 seconds and one retry. The README SHALL document it.

#### Scenario: Default
- **WHEN** `SCRAPER_TIMEOUT` is unset
- **THEN** a scrape is bounded by 360 seconds

### Requirement: Page result mapping
`internal/scraper` `mapResult` SHALL map `url_kind: page` to `Job.input`: `content` the `text`, `source.url` the `canonical_url` when its registered domain equals `domain` and otherwise the `final_url` (the requested URL when that is empty), `source.title`, `source.author` and `source.published_at` from the page when not null, `source.platform` `web`. A `page` result without a string `text` SHALL fail with `scraper_unavailable`, and a blank `text` with `no_text_content`.

#### Scenario: Same-domain canonical URL
- **WHEN** a page fetched with a tracking query declares the same URL without it as canonical on its own domain
- **THEN** `source.url` is the canonical URL

#### Scenario: Canonical URL on another domain
- **WHEN** the canonical URL's registered domain differs from `domain`
- **THEN** `source.url` is the final URL

#### Scenario: Outlet of a news page
- **WHEN** a `dw.com` article is scraped and `data/outlets.csv` is the outlet table
- **THEN** the outlet matched for `source.url` is Deutsche Welle, country `DE`, bias `center`

### Requirement: Web credentials never exposed
The Oxylabs credentials SHALL appear only in the provider request headers. They SHALL NOT appear in any log line, scrape error `message` or `details`, or job output; the secret values SHALL be redacted from logs through the redactor; the module SHALL NOT log request headers or bodies and SHALL NOT put provider answer bodies into errors.

#### Scenario: Key rejected
- **WHEN** the provider answers 401 with a body containing the API key
- **THEN** the scrape fails with `upstream_error` and neither the error nor the job output contains the key

#### Scenario: Sentinel key
- **WHEN** jobs run end to end with a sentinel `OXYLABS_WEB_API_KEY`
- **THEN** the sentinel appears in no log line and no job

### Requirement: Offline web tests
Tests of the web module SHALL use a stub provider routed in memory (`scrapertest`) and recorded HTML fixtures (news article, JS shell, login wall, empty page, Markdown only) in `internal/socialscraper/web/testdata`, and SHALL NOT access `oxylabs.io`.

#### Scenario: Unrouted provider
- **WHEN** a test reaches a provider host that has no route
- **THEN** the test fails

### Requirement: Web README
`README.md` SHALL document the `web` platform and `page` URL kind, the `OXYLABS_*` and `WEB_*` variables with defaults, the cost controls (at most 3 requests per scrape, cache, rendering only on short text) and that Oxylabs is never called in tests.

#### Scenario: Operator reads the section
- **WHEN** an operator reads the "Scraping" section
- **THEN** it names every variable of "Web page configuration" and states that tests never call Oxylabs

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
