# scraper-facebook-x Specification

## Purpose
Backend-local runtime of the Facebook and X platform modules of the scraper service that implements the shared social-scraper-api: browser handling for public Facebook pages, the twscrape account pool for X, secret handling and offline test rules.

## Requirements

### Requirement: Facebook browser module
The Facebook module SHALL drive headless chromium through `github.com/chromedp/chromedp`, started with `--no-sandbox` and `--headless` using the chromium binary on `PATH`. It SHALL open only `https://www.facebook.com/` URLs rebuilt from the recognized page and post ids, SHALL collect the bodies of the page's own `/api/graphql/` responses through CDP network events, and SHALL parse `Page`, `Story`, `Comment` and `User` nodes from them.

#### Scenario: Page profile from GraphQL
- **WHEN** a client scrapes `https://www.facebook.com/examplenews` and the page's GraphQL responses carry a `Page` node and three `Story` nodes
- **THEN** the response is 200 with `platform: facebook`, `url_kind: profile`, `username: examplenews` and the stories as `posts`, newest first

#### Scenario: Page post with comments
- **WHEN** a client scrapes `https://m.facebook.com/examplenews/posts/9003?ref=x` and the GraphQL responses carry that story and two comments
- **THEN** the browser opens `https://www.facebook.com/examplenews/posts/9003`, `post` is that story and `comments` are the two comments oldest first, each with `in_reply_to_id`

#### Scenario: Browser closed after a failure
- **WHEN** navigation fails with a timeout or another error, or the scrape is cancelled by the upstream timeout
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

### Requirement: X credentials not configured
When either `TWITTER_AUTH_TOKEN` or `TWITTER_CT0` is unset or empty, the X module SHALL answer 503 `platform_not_configured` with `details.platform` `twitter` and `details.missing_settings` naming exactly the unset variables, before any request to X is made. Other platforms SHALL be unaffected.

#### Scenario: One variable unset
- **WHEN** `TWITTER_CT0` is unset and a client scrapes `https://x.com/alice`
- **THEN** the status is 503 `platform_not_configured` with `details.missing_settings` `["TWITTER_CT0"]`, no variable value in the body and no request was sent to X

### Requirement: X profile and post scraping
A profile scrape SHALL be filled from the user lookup and the user's own tweets (retweets skipped, newest first, at most `max_posts`, following the bottom cursor while more are needed). A post scrape SHALL be filled from the tweet detail, its direct replies (`in_reply_to` equal to the post id, oldest first, at most `max_comments`, no reply request when 0) and the author's own tweets. HTML entities in text and bio SHALL be decoded.

#### Scenario: Profile with a retweet
- **WHEN** a client scrapes `https://x.com/alice` with `max_posts` 2 and the timeline has a retweet by alice
- **THEN** `posts` has her two newest own tweets and no retweet

#### Scenario: Post with replies
- **WHEN** a client scrapes `https://mobile.x.com/alice/status/1790000000000000000?s=20` and two replies are served newest first
- **THEN** `comments` are the two replies oldest first, each with `in_reply_to_id` `1790000000000000000`

### Requirement: X error mapping
A failed history or replies request SHALL become a `history_unavailable` or `comments_unavailable` warning. A missing user or tweet SHALL answer 404 `account_not_found` or `post_not_found`, a protected account 403 `account_private`, a deadline 504 `upstream_timeout`, and any other failure, including 401 or 403 for rejected cookies, 502 `upstream_error` with a fixed message.

#### Scenario: Protected account
- **WHEN** the looked-up user is protected
- **THEN** the status is 403 `account_private` with `details.platform` `twitter`

#### Scenario: Replies fail
- **WHEN** the request for more replies fails after the tweet was fetched
- **THEN** the response is 200 with `comments: []` and a `comments_unavailable` warning

### Requirement: Credentials never exposed
The values of `TWITTER_AUTH_TOKEN` and `TWITTER_CT0` SHALL NOT appear in any response body, response header, log line or captured output, including when an upstream error or a panic carries them; errors SHALL carry fixed messages and Go error type names only, never upstream error text.

#### Scenario: Sentinel credentials
- **WHEN** both variables hold sentinel strings and X and Facebook scrapes succeed, fail and panic with messages containing the sentinels
- **THEN** neither sentinel appears in any response, header or log record

### Requirement: Offline module tests
Tests of the Facebook and X modules SHALL use the fixtures under `internal/socialscraper/facebook/testdata/` and `internal/socialscraper/x/testdata/`, a fake browser for Facebook and routed in-memory HTTP for X; no test SHALL start a browser or contact the network, and a Facebook test that reaches the real chromedp launcher without installing a fake SHALL fail.

#### Scenario: Unfaked browser
- **WHEN** a test reaches the real chromedp launcher without installing a fake
- **THEN** the launcher panics under `testing.Testing()` instead of starting chromium

### Requirement: Image carries chromium
`scraper.Dockerfile` SHALL install chromium from the distribution's apt packages, which is the binary the Facebook module finds on `PATH`, and the image SHALL still contain no secret.

#### Scenario: Image build
- **WHEN** the image is built from `scraper.Dockerfile`
- **THEN** `chromium` is on `PATH` for the unprivileged runtime user

### Requirement: Facebook browser abstraction
The Facebook module SHALL reach the browser only through an internal interface (navigate with HTTP status, collected GraphQL bodies, visible text, close). The real launcher SHALL refuse to start under `testing.Testing()` unless a test installs a fake. The browser SHALL be closed on every path, including errors, panics and context cancellation.

#### Scenario: Closed after every outcome
- **WHEN** a scrape succeeds, fails with a typed error, panics or is cancelled
- **THEN** `Close` was called on the browser

### Requirement: X GraphQL client
The X module SHALL call the web GraphQL operations `UserByScreenName`, `UserTweets` and `TweetDetail` at `https://x.com/i/api/graphql/<queryId>/<operation>` with the public web-app bearer, the cookies `auth_token` and `ct0`, and the header `x-csrf-token` equal to `ct0`. Query ids, feature flags and the bearer SHALL be defined in the single file `x/endpoints.go`. There SHALL be no account-pool database and `SCRAPER_X_POOL_DB` SHALL NOT be read.

#### Scenario: Request shape
- **WHEN** a profile is scraped with `TWITTER_AUTH_TOKEN` `a` and `TWITTER_CT0` `c`
- **THEN** every request goes to `x.com` under `/i/api/graphql/`, carries cookies `auth_token=a` and `ct0=c` and `x-csrf-token: c`

### Requirement: Facebook and X registration
`cmd/scraper/platforms_fbx.go` SHALL register the Facebook module for `facebook` and the X module for `twitter`.

#### Scenario: Registered
- **WHEN** the scraper registers its platforms
- **THEN** the registry has modules for `facebook` and `twitter`

### Requirement: X rate limiting
HTTP 429 from X SHALL answer 429 `rate_limited`, with `Retry-After` and `details.retry_after_s` derived from the `x-rate-limit-reset` header when present.

#### Scenario: Throttled
- **WHEN** X answers 429 with `x-rate-limit-reset` 90 seconds ahead
- **THEN** the status is 429 `rate_limited` with `Retry-After` 90 and `details.retry_after_s` 90

### Requirement: Facebook scrape timeout
Facebook page work SHALL stop when the scrape context ends: a navigation timeout SHALL answer 504 `upstream_timeout` and any other navigation or launch error 502 `upstream_error`.

#### Scenario: Navigation timeout
- **WHEN** the navigation does not finish before the scrape context ends
- **THEN** the status is 504 `upstream_timeout` and the browser is closed
