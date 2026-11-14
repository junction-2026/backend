## Purpose
Backend-local behaviour of the Instagram and Threads modules of the scraper service: how they obtain data, how the optional Instagram session is handled, how upstream failures map to the shared scraper error codes, and how they are tested offline.

## ADDED Requirements

### Requirement: Instagram access through instaloader
The Instagram module SHALL use instaloader anonymously for public profiles and posts, bounded by `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`, without instaloader's sleeping and retry back-off. It SHALL return the profile members, `posts` up to `max_posts` and, for a post URL, the post and its comments up to `max_comments`. A counter or flag that Instagram does not state, or hides, SHALL be null, not 0.

#### Scenario: Public profile
- **WHEN** a client scrapes a public Instagram profile with `max_posts` 2
- **THEN** the ScrapeResult has the profile members, the 2 newest posts and `comments: []`

#### Scenario: Hidden likes
- **WHEN** a post has like counts disabled
- **THEN** its Post has `likes: null`

#### Scenario: Counters missing
- **WHEN** the profile data lacks the follower, following and post counts
- **THEN** those members are null and the response is 200 with a `partial_parse` warning

#### Scenario: Comments need a login
- **WHEN** a post URL is scraped without an Instagram session
- **THEN** the response is 200 with the post, `comments: []` and a `comments_unavailable` warning

#### Scenario: Comments with a session
- **WHEN** a post URL is scraped with a session and `max_comments` 2
- **THEN** the response has the 2 oldest comments, replies carrying `in_reply_to_id` of their parent

### Requirement: Optional Instagram session
The Instagram module SHALL load an instaloader session from the file named by the environment variable `INSTAGRAM_SESSION_FILE` when it is set, and SHALL scrape anonymously when it is unset or the file cannot be loaded. The path and content of the session file MUST NOT appear in any response, header or log line.

#### Scenario: Session loaded
- **WHEN** `INSTAGRAM_SESSION_FILE` names a valid session file `session-viewer`
- **THEN** the scrape runs as the logged-in user `viewer` and no log line contains the file path or its cookies

#### Scenario: Broken session file
- **WHEN** `INSTAGRAM_SESSION_FILE` names a file that is not a session
- **THEN** the scrape runs anonymously and the log line does not contain the path

### Requirement: Instagram failure mapping
The Instagram module SHALL map failures to the shared error codes: private profile `account_private`; HTTP 429 or a login wall `rate_limited` (with retry information only when the wait is known); missing account or post `account_not_found` or `post_not_found`; timeout `upstream_timeout`; anything else `upstream_error`. A failure while reading the history SHALL keep the posts already read and add a warning.

#### Scenario: Private account
- **WHEN** the profile is private
- **THEN** the status is 403 `account_private` with `details.platform` `instagram`

#### Scenario: Throttled
- **WHEN** instaloader fails with a connection error caused by HTTP 429
- **THEN** the status is 429 `rate_limited` without a `Retry-After` header

#### Scenario: Login wall
- **WHEN** Instagram demands a login for the profile
- **THEN** the status is 429 `rate_limited`

#### Scenario: Missing account
- **WHEN** Instagram has no such profile
- **THEN** the status is 404 `account_not_found`

#### Scenario: Timeout
- **WHEN** an Instagram request times out
- **THEN** the status is 504 `upstream_timeout`

#### Scenario: Throttled while reading history
- **WHEN** the second post of the history is refused with 429
- **THEN** the response is 200 with the first post and a `history_truncated` warning

### Requirement: Threads access through embedded page data
The Threads module SHALL fetch the public page with `httpx` and a browser User-Agent, without following redirects, and SHALL find the profile, posts and comments by searching the JSON embedded in the page for user-like and post-like objects. Every member access SHALL be defensive: a missing member is null and adds a `partial_parse` warning; `following` and `post_count` are always null because Threads does not expose them.

#### Scenario: Profile with recent posts
- **WHEN** a client scrapes a Threads profile with `max_posts` 2
- **THEN** the ScrapeResult has the profile members and the 2 newest posts embedded in the page

#### Scenario: Post with replies
- **WHEN** a client scrapes a Threads post URL with replies embedded in the page
- **THEN** the response has `post`, the replies as comments oldest first up to `max_comments`, and the author's recent posts from the profile page

#### Scenario: Renamed or missing keys
- **WHEN** the page lacks the follower count, renames the biography and has one post without a code
- **THEN** the response is 200 with `followers: null`, `bio: null`, the remaining posts and `partial_parse` warnings, never a 500

#### Scenario: Post object without a code
- **WHEN** a post-like object in the page has no `code`
- **THEN** it is skipped and the response carries a `partial_parse` warning

#### Scenario: Profile page of the author unavailable
- **WHEN** the profile page request for a post's author fails
- **THEN** the response is 200 with the post and a `history_unavailable` warning

### Requirement: Threads failure mapping
The Threads module SHALL answer `account_not_found` or `post_not_found` for HTTP 404, `rate_limited` for HTTP 429 (with the `Retry-After` value when sent), `account_private` for a private account, `upstream_timeout` when the request times out, and `upstream_error` for any other status, any redirect, a connection failure, or a page that contains no recognizable profile or post data.

#### Scenario: Unrecognizable page
- **WHEN** the page contains no recognizable profile data
- **THEN** the status is 502 `upstream_error` with `details.platform` `threads`

#### Scenario: Rate limit with wait
- **WHEN** Threads answers 429 with `Retry-After: 120`
- **THEN** the status is 429 `rate_limited` with `details.retry_after_s` 120

### Requirement: Offline recorded-fixture tests
Tests of the Instagram and Threads modules SHALL use recorded fixtures under `scraper/tests/fixtures/instagram/` and `scraper/tests/fixtures/threads/` and SHALL fail instead of opening a network connection.

#### Scenario: No network
- **WHEN** a test tries to open a socket connection
- **THEN** the test fails with an error naming the offline rule
