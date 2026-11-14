## ADDED Requirements

### Requirement: Source info adapter
The `source_info` adapter SHALL read the bound `news_search` result (`search`) and the bound `$input.source` URL, normalize each host, match it by exact host then by registered domain (eTLD+1) against the outlet table as the shared capability `source-info-result` specifies, and return the SourceInfo object (`articles`, `page_outlet`, `summary`, `table`). It SHALL NOT make network requests. Outlet objects SHALL contain only `name`, `domain`, `country_code`, `country`, `bias` and `factuality`.

#### Scenario: Registered domain, host and www
- **WHEN** a 5-row table lists BBC News (domains `bbc.co.uk|bbc.com`) and The Hindu (host `sportstar.thehindu.com`), and the URLs are `https://news.bbc.co.uk/x`, `https://sportstar.thehindu.com/x` and `https://WWW.BBC.COM./x`
- **THEN** they match BBC News (`match: domain`), The Hindu (`match: host`) and BBC News with `domain: bbc.com`

#### Scenario: Unknown host
- **WHEN** a URL is `https://unknown.example/x`
- **THEN** its entry is `{"url": ..., "domain": "unknown.example", "matched": false}` with no `outlet` and no `match`

#### Scenario: Twelve articles
- **WHEN** 12 articles are found, 7 of them matched
- **THEN** `articles` has 12 entries in `news_search` order, `summary.articles` is 12, `summary.matched` is 7, `summary.bias` and `summary.countries` count the matched outlets' values (`unknown` and `""` included, only keys above 0, each summing to 7) and `summary.outlets` is the number of distinct outlets

#### Scenario: Page outlet without articles
- **WHEN** `input.source.url` is `https://www.spiegel.de/politik/a-1.html`, the table has `spiegel.de` and `news_search` returned `articles: []`
- **THEN** the result has `page_outlet` set, `articles: []` and `summary.matched` 0

### Requirement: Source info skip
When `news_search` succeeded with no articles and `input.source.url` is absent or blank, the adapter SHALL return the skip outcome `no_sources` (the mechanism of the `news_search` skip), so the run settles as `skipped` with `result: null` after `analyzer.started`.

#### Scenario: Nothing to look up
- **WHEN** `articles` is `[]` and the job has no `input.source.url`
- **THEN** the skip outcome with reason `no_sources` is returned

### Requirement: Source info failures
The adapter SHALL fail with `adapter_error` when the `news_search` result is not a NewsSearch object (for example `articles` is not an array) and with `cancelled` when its context was cancelled. An unparsable article URL SHALL be unmatched with `domain: null` and SHALL NOT fail the analyzer.

#### Scenario: Malformed result
- **WHEN** the bound result has `articles: "x"`
- **THEN** the adapter returns `adapter_error`

#### Scenario: Unparsable URL
- **WHEN** an article URL is `::bad`
- **THEN** its entry has `matched: false` and `domain: null` and the analyzer succeeds

### Requirement: Source info test coverage
The backend tests SHALL cover: matching with a 5-row CSV, three startup-validation cases naming the row, the 12-article summary, `page_outlet` with an empty `news_search` result, the `no_sources` skip, an unparsable URL, and loading the real `data/outlets.csv` (more than 5000 outlets, no error, `table.source` `outlets.csv`).

#### Scenario: Suite
- **WHEN** `go test ./...` runs
- **THEN** these cases pass
