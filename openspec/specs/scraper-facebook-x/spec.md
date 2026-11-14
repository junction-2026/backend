# scraper-facebook-x Specification

## Purpose
Backend-local runtime of the Facebook and X platform modules of the scraper service that implements the shared social-scraper-api: browser handling for public Facebook pages, the twscrape account pool for X, secret handling and offline test rules.

## Requirements

### Requirement: X credentials not configured
When either `TWITTER_AUTH_TOKEN` or `TWITTER_CT0` is unset or empty, the X module SHALL fail with `platform_not_configured` with `details.platform` `twitter` and `details.missing_settings` naming exactly the unset variables, before any request to X is made. Other platforms SHALL be unaffected.

#### Scenario: One variable unset
- **WHEN** `TWITTER_CT0` is unset and `https://x.com/alice` is scraped
- **THEN** the scrape fails with `platform_not_configured` and `details.missing_settings` `["TWITTER_CT0"]`, no variable value is in the error and no request was sent to X

### Requirement: X profile and post scraping
A profile scrape SHALL be filled from the user lookup and the user's own tweets (retweets skipped, newest first, at most `max_posts`, following the bottom cursor while more are needed). A post scrape SHALL be filled from the tweet detail, its direct replies (`in_reply_to` equal to the post id, oldest first, at most `max_comments`, no reply request when 0) and the author's own tweets. HTML entities in text and bio SHALL be decoded.

#### Scenario: Profile with a retweet
- **WHEN** a client scrapes `https://x.com/alice` with `max_posts` 2 and the timeline has a retweet by alice
- **THEN** `posts` has her two newest own tweets and no retweet

#### Scenario: Post with replies
- **WHEN** a client scrapes `https://mobile.x.com/alice/status/1790000000000000000?s=20` and two replies are served newest first
- **THEN** `comments` are the two replies oldest first, each with `in_reply_to_id` `1790000000000000000`

### Requirement: X error mapping
A failed history or replies request SHALL become a `history_unavailable` or `comments_unavailable` warning. A missing user or tweet SHALL fail with `account_not_found` or `post_not_found`, a protected account with `account_private`, a deadline with `upstream_timeout`, and any other failure, including 401 or 403 for rejected cookies, with `upstream_error` and a fixed message.

#### Scenario: Protected account
- **WHEN** the looked-up user is protected
- **THEN** the scrape fails with `account_private` and `details.platform` `twitter`

#### Scenario: Replies fail
- **WHEN** the request for more replies fails after the tweet was fetched
- **THEN** the scrape succeeds with `comments: []` and a `comments_unavailable` warning

### Requirement: Credentials never exposed
The values of `TWITTER_AUTH_TOKEN` and `TWITTER_CT0` SHALL NOT appear in any scrape result, scrape error, job, event, API response, log line or captured output, including when an upstream error or a panic carries them; errors SHALL carry fixed messages and Go error type names only, never upstream error text.

#### Scenario: Sentinel credentials
- **WHEN** both variables hold sentinel strings and X scrapes succeed, fail and panic with messages containing the sentinels
- **THEN** neither sentinel appears in any scrape result, error or log record

### Requirement: X GraphQL client
The X module SHALL call the web GraphQL operations `UserByScreenName`, `UserTweets` and `TweetDetail` at `https://x.com/i/api/graphql/<queryId>/<operation>` with the public web-app bearer, the cookies `auth_token` and `ct0`, and the header `x-csrf-token` equal to `ct0`. Query ids, feature flags and the bearer SHALL be defined in the single file `x/endpoints.go`. There SHALL be no account-pool database and `SCRAPER_X_POOL_DB` SHALL NOT be read.

#### Scenario: Request shape
- **WHEN** a profile is scraped with `TWITTER_AUTH_TOKEN` `a` and `TWITTER_CT0` `c`
- **THEN** every request goes to `x.com` under `/i/api/graphql/`, carries cookies `auth_token=a` and `ct0=c` and `x-csrf-token: c`

### Requirement: X rate limiting
HTTP 429 from X SHALL fail the scrape with `rate_limited`, with `details.retry_after_s` derived from the `x-rate-limit-reset` header when present.

#### Scenario: Throttled
- **WHEN** X answers 429 with `x-rate-limit-reset` 90 seconds ahead
- **THEN** the scrape fails with `rate_limited` and `details.retry_after_s` 90

### Requirement: X module registration
`cmd/orchestrator` SHALL register the X module for `twitter` when it builds the scraper registry at startup, passing the shared `*http.Client` and the process environment for `TWITTER_AUTH_TOKEN` and `TWITTER_CT0`.

#### Scenario: Registered
- **WHEN** the orchestrator starts with scraping enabled
- **THEN** the registry has a module for `twitter` and none for any platform outside `bluesky`, `mastodon`, `instagram`, `threads` and `twitter`

### Requirement: Offline X module tests
Tests of the X module SHALL use the fixtures under `internal/socialscraper/x/testdata/` and routed in-memory HTTP; no test SHALL contact the network.

#### Scenario: Routed requests only
- **WHEN** the X module tests run
- **THEN** every request is served by the `scrapertest` router from fixtures
