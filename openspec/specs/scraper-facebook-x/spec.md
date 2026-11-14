# scraper-facebook-x Specification

## Purpose
Backend-local runtime of the Facebook and X platform modules of the scraper service that implements the shared social-scraper-api: browser handling for public Facebook pages, the twscrape account pool for X, secret handling and offline test rules.

## Requirements

### Requirement: Facebook browser module
The Facebook module SHALL read public pages and page posts with headless chromium through Playwright started with `--no-sandbox`, SHALL parse the GraphQL responses the page itself requests, and SHALL open only `https://www.facebook.com/` URLs rebuilt from the recognized page and post ids. It SHALL stop page work after a share of `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`, answering 504 `upstream_timeout` when the page does not load in time, and SHALL close the browser on every path.

#### Scenario: Page profile from GraphQL
- **WHEN** a client scrapes `https://www.facebook.com/examplenews` and the page's GraphQL responses carry a `Page` node and three `Story` nodes
- **THEN** the response is 200 with `platform: facebook`, `url_kind: profile`, `username: examplenews` and the stories as `posts`, newest first

#### Scenario: Page post with comments
- **WHEN** a client scrapes `https://www.facebook.com/examplenews/posts/9003` and the GraphQL responses carry that story and two comments
- **THEN** `post` is that story and `comments` are the two comments oldest first, each with `in_reply_to_id`

#### Scenario: Browser closed after a failure
- **WHEN** navigation fails with a Playwright timeout or error, or the scrape is cancelled by the upstream timeout
- **THEN** the browser is closed and the status is 504 `upstream_timeout` or 502 `upstream_error` respectively

### Requirement: Facebook personal profiles refused
The Facebook module SHALL NOT return data of a personal profile: a `profile.php` URL, and a vanity URL whose page data is a `User` node of that URL, SHALL answer 422 `unsupported_account_type` with `details.account_type` `personal_profile`. A `User` node of a commenter SHALL NOT make a page unsupported.

#### Scenario: Vanity URL of a person
- **WHEN** a client scrapes `https://www.facebook.com/jane.doe` and the page data is a `User` node with vanity `jane.doe`
- **THEN** the status is 422 `unsupported_account_type` with `details.account_type` `personal_profile`

#### Scenario: Commenters on a page post
- **WHEN** a page post's comments are written by `User` nodes
- **THEN** the page is scraped normally

### Requirement: Facebook missing or unreadable pages
When the page answers 404 or shows that its content is not available the module SHALL answer 404 `account_not_found` (or `post_not_found` for a post URL); when the page loads without the requested post it SHALL answer 404 `post_not_found`; when no recognizable page data arrives it SHALL answer 502 `upstream_error`. A missing follower count SHALL become `followers: null` with a `partial_parse` warning, and a post page without comments a `comments_unavailable` warning.

#### Scenario: Unavailable page
- **WHEN** the navigation answers 404
- **THEN** the status is 404 `account_not_found` with `details.platform` `facebook`

#### Scenario: Login wall
- **WHEN** the page loads without any GraphQL page data and without an unavailable-content text
- **THEN** the status is 502 `upstream_error`

### Requirement: X account pool
The X module SHALL use twscrape with one account whose cookies are `TWITTER_AUTH_TOKEN` and `TWITTER_CT0`, built from the environment on the first X scrape and reused afterwards. The pool database SHALL live in a private temporary directory or at the path in `SCRAPER_X_POOL_DB`, never inside the repository.

#### Scenario: Pool from environment
- **WHEN** both variables are set and an X URL is scraped for the first time
- **THEN** the pool has one active account with cookies `auth_token` and `ct0` and its database file is under the temp directory or `SCRAPER_X_POOL_DB`

### Requirement: X credentials not configured
When either `TWITTER_AUTH_TOKEN` or `TWITTER_CT0` is unset or empty, the X module SHALL answer 503 `platform_not_configured` with `details.platform` `twitter` and `details.missing_settings` naming the unset variables, before any pool exists. Other platforms SHALL be unaffected.

#### Scenario: One variable unset
- **WHEN** `TWITTER_CT0` is unset and a client scrapes `https://x.com/alice`
- **THEN** the status is 503 `platform_not_configured` with `details.missing_settings` `["TWITTER_CT0"]` and no variable value in the body

### Requirement: X profile and post scraping
The X module SHALL fill a profile scrape from the user lookup and the user's own tweets (retweets skipped, newest first, at most `max_posts`), and a post scrape from the tweet details, its direct replies (oldest first, at most `max_comments`, none requested when 0) and the author's own tweets. HTML entities in text and bio SHALL be decoded.

#### Scenario: Profile with a retweet
- **WHEN** a client scrapes `https://x.com/alice` with `max_posts` 2 and the timeline has a retweet by alice
- **THEN** `posts` has her two newest own tweets and no retweet

#### Scenario: Post with replies
- **WHEN** a client scrapes `https://mobile.x.com/alice/status/1790000000000000000?s=20` and two replies are served newest first
- **THEN** `comments` are the two replies oldest first, each with `in_reply_to_id` `1790000000000000000`

### Requirement: X error mapping
A failed history or replies request SHALL become a `history_unavailable` or `comments_unavailable` warning. A missing user or tweet SHALL answer 404 `account_not_found` or `post_not_found`, a protected account 403 `account_private`, a throttled pool 429 `rate_limited`, a timeout 504 `upstream_timeout` and any other twscrape failure 502 `upstream_error`.

#### Scenario: Protected account
- **WHEN** the looked-up user is protected
- **THEN** the status is 403 `account_private` with `details.platform` `twitter`

#### Scenario: Replies fail
- **WHEN** the replies request raises after the tweet was fetched
- **THEN** the response is 200 with `comments: []` and a `comments_unavailable` warning

### Requirement: Credentials never exposed
The values of `TWITTER_AUTH_TOKEN` and `TWITTER_CT0` SHALL NOT appear in any response body, response header, log line or captured output, including when twscrape or Playwright raise exceptions whose text contains them; responses and logs SHALL carry fixed messages and exception class names only.

#### Scenario: Sentinel credentials
- **WHEN** both variables hold sentinel strings, X scrapes succeed, fail and raise exceptions containing the sentinels
- **THEN** neither sentinel appears in any response, header, log record or stdout/stderr output

### Requirement: Offline module tests
Tests of the Facebook and X modules SHALL use recorded fixtures under `scraper/tests/fixtures/facebook/` and `scraper/tests/fixtures/x/` with a fake Playwright and a fake twscrape API; no test SHALL start a browser or contact the network, and a test that forgets to fake either module SHALL fail.

#### Scenario: Unfaked browser
- **WHEN** a test reaches `async_playwright` without installing a fake
- **THEN** the test fails with an assertion instead of starting chromium

### Requirement: Image carries chromium
`scraper/Dockerfile` SHALL install chromium and its system libraries for Playwright into a path readable by the unprivileged runtime user, and the image SHALL still contain no secret.

#### Scenario: Image build
- **WHEN** the image is built from `scraper/Dockerfile`
- **THEN** `playwright install --with-deps chromium` has run and `/ms-playwright` is world-readable
