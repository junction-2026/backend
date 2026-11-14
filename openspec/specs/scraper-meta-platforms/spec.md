# scraper-meta-platforms Specification

## Purpose
Backend-local behaviour of the Instagram and Threads modules of the scraper service: how they obtain data, how the optional Instagram session is handled, how upstream failures map to the shared scraper error codes, and how they are tested offline.

## Requirements

### Requirement: Instagram access through public web endpoints
The Instagram module SHALL read public profiles and posts from Instagram's public web JSON endpoints with `net/http`: the profile from `GET https://www.instagram.com/api/v1/users/web_profile_info/?username=<user>` with the `X-IG-App-ID` web header, a post and its comments from media endpoints with the shortcode decoded to the media id. It SHALL contact only `www.instagram.com` and `i.instagram.com` and keep every endpoint path and header in `endpoints.go`.

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
- **WHEN** a post or reel URL is scraped without an Instagram session
- **THEN** the response is 200 with the post, `comments: []` and a `comments_unavailable` warning

#### Scenario: Comments with a session
- **WHEN** a post URL is scraped with a session and `max_comments` 2
- **THEN** the response has the 2 oldest comments, and with a larger `max_comments` the replies carry `in_reply_to_id` of their parent

#### Scenario: Only Instagram hosts
- **WHEN** any Instagram scrape runs
- **THEN** every request goes to `www.instagram.com` or `i.instagram.com` and redirects are not followed

### Requirement: Optional Instagram session
The Instagram module SHALL read `INSTAGRAM_SESSION_FILE`, when set, as JSON `{"sessionid": string, "csrftoken": string, "ds_user_id": string}` and send it to Instagram as cookies. When the file is missing, unreadable or lacks `sessionid` it SHALL log one fixed warning without the path and scrape anonymously. The path and content of the file MUST NOT appear in any response, header or log line.

#### Scenario: Session loaded
- **WHEN** `INSTAGRAM_SESSION_FILE` names a valid session file
- **THEN** Instagram requests carry its cookies, comments are requested, and no response, header or log line contains the file path or a cookie value

#### Scenario: Broken session file
- **WHEN** `INSTAGRAM_SESSION_FILE` names a file that is missing, not JSON or without `sessionid`
- **THEN** the scrape runs anonymously without cookies and the one warning log line does not contain the path

### Requirement: Instagram failure mapping
The Instagram module SHALL map failures to the shared error codes: private profile 403 `account_private`; a login wall 503 `login_required`; HTTP 429 429 `rate_limited`; a 404 or empty answer 503 `login_required` without a session, 404 `account_not_found` or `post_not_found` with one; a deadline 504 `upstream_timeout`; else 502 `upstream_error`. A failed history or comments read SHALL keep what was read and add `history_unavailable`, `history_truncated` or `comments_unavailable`.

#### Scenario: Private account
- **WHEN** the profile is private
- **THEN** the status is 403 `account_private` with `details.platform` `instagram`

#### Scenario: Throttled
- **WHEN** Instagram answers HTTP 429 without `Retry-After`
- **THEN** the status is 429 `rate_limited` without a `Retry-After` header

#### Scenario: Throttled with wait
- **WHEN** Instagram answers HTTP 429 with `Retry-After: 90`
- **THEN** the status is 429 `rate_limited` with `Retry-After: 90`

#### Scenario: Login wall
- **WHEN** Instagram redirects the profile request to `/accounts/login`, answers 401, or answers 200 with `"require_login": true`
- **THEN** the status is 503 `login_required` with `details.platform` `instagram`, and not `rate_limited`; with no session loaded `details.missing_settings` is `["INSTAGRAM_SESSION_FILE"]`

#### Scenario: Anonymous 404
- **WHEN** no session file is loaded and `web_profile_info` or the media endpoint answers 404, or answers 200 without a user or media item
- **THEN** the status is 503 `login_required` with `details.platform` `instagram` and `details.missing_settings` `["INSTAGRAM_SESSION_FILE"]`

#### Scenario: Refused feed read
- **WHEN** no session file is loaded and the feed endpoint answers 404 after the profile was read
- **THEN** the response is 200 with the profile, `posts: []` and a `history_unavailable` warning

#### Scenario: Missing account
- **WHEN** a session file is loaded and Instagram has no such profile
- **THEN** the status is 404 `account_not_found`

#### Scenario: Missing post
- **WHEN** a session file is loaded and Instagram has no media for the shortcode
- **THEN** the status is 404 `post_not_found`

#### Scenario: Comments cut short
- **WHEN** reading the comments fails after the first page
- **THEN** the response is 200 with the comments already read and a `comments_unavailable` warning

#### Scenario: Plain text
- **WHEN** a caption, comment or biography has leading or trailing whitespace
- **THEN** the returned `text` and `bio` are trimmed

#### Scenario: Timeout
- **WHEN** an Instagram request does not answer before the scrape deadline
- **THEN** the status is 504 `upstream_timeout`

#### Scenario: Other failure
- **WHEN** Instagram answers HTTP 500 or data that is not JSON
- **THEN** the status is 502 `upstream_error`

#### Scenario: Throttled while reading history
- **WHEN** the second page of the history is refused with 429
- **THEN** the response is 200 with the first page's posts and a `history_truncated` warning

#### Scenario: History unavailable
- **WHEN** the first page of the history fails
- **THEN** the response is 200 with the profile, `posts: []` and a `history_unavailable` warning

### Requirement: Threads access through embedded page data
The Threads module SHALL fetch `/@<user>` or `/@<user>/post/<code>` of `https://www.threads.com`, whatever host the URL named, with `net/http`, a browser User-Agent and no redirects, extract the JSON of the page's `<script type="application/json">` elements with `golang.org/x/net/html`, and search it for user-like and post-like objects. A missing member SHALL be null with a `partial_parse` warning; `following` and `post_count` are always null.

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
- **THEN** it is skipped and the response carries one `partial_parse` warning for the profile and post pages together

#### Scenario: Profile page of the author unavailable
- **WHEN** the profile page request for a post's author fails
- **THEN** the response is 200 with the post and a `history_unavailable` warning

#### Scenario: threads.net URL
- **WHEN** a client scrapes `https://www.threads.net/@alice`
- **THEN** the module requests `https://www.threads.com/@alice`, does not follow redirects, and builds `profile_url` and `post.url` on `https://www.threads.com`

### Requirement: Threads failure mapping
The Threads module SHALL answer `account_not_found` or `post_not_found` for HTTP 404, `rate_limited` for HTTP 429 (with the `Retry-After` value when sent), `account_private` for a private account, `upstream_timeout` when the request times out, `login_required` for a 200 page without data that shows a login wall, and `upstream_error` for any other status, any redirect, a connection failure, or a page with neither data nor login markers.

#### Scenario: Unrecognizable page
- **WHEN** the page contains no recognizable profile data and no login markers
- **THEN** the status is 502 `upstream_error` with `details.platform` `threads` and the message `the Threads page contained no recognizable profile data` (`post` for a post page)

#### Scenario: Login wall page
- **WHEN** the page contains no recognizable profile or post data and shows a login form, a link to `/login` or login-required JSON flags
- **THEN** the status is 503 `login_required` with `details.platform` `threads` and no `details.missing_settings`, because Threads has no session setting

#### Scenario: Rate limit with wait
- **WHEN** Threads answers 429 with `Retry-After: 120`
- **THEN** the status is 429 `rate_limited` with `details.retry_after_s` 120

### Requirement: Offline recorded-fixture tests
Tests of the Instagram and Threads modules SHALL use fixtures under `internal/socialscraper/instagram/testdata` and `internal/socialscraper/threads/testdata`, served through the `scrapertest` router so that a request to an unrouted host fails the test. Instagram fixtures SHALL have the raw shape of the web endpoints' answers, hand-built from recorded data, with no real cookie or session value.

#### Scenario: No network
- **WHEN** a test makes a request to a host that has no route
- **THEN** the test fails with an error naming the unrouted host

### Requirement: Instagram result content
The Instagram module SHALL return the profile members, `posts` newest first up to `max_posts` (paging while Instagram offers more) and, for a post or reel URL, the post and its comments oldest first up to `max_comments`, a reply carrying `in_reply_to_id` of its parent. A counter or flag that Instagram does not state, or hides, SHALL be null, never 0.

#### Scenario: History paging
- **WHEN** a profile is scraped with `max_posts` 3 and Instagram serves the posts in two pages
- **THEN** the response has the 3 newest posts, and with `max_posts` 2 the second page is not requested

### Requirement: Meta platform registration and client use
`cmd/orchestrator` SHALL register the Instagram module for `instagram` (with `INSTAGRAM_SESSION_FILE`) and the Threads module for `threads` when it builds the scraper registry at startup. Both modules SHALL use only the shared `*http.Client` injected from the core and SHALL run under the scrape context.

#### Scenario: Scrape through the server
- **WHEN** jobs are created with `https://www.instagram.com/reel/C1a2b3c4d5/` and `https://www.threads.net/@alice` against the orchestrator with routed fixtures
- **THEN** both scrapes succeed with `platform` `instagram` / `threads` and `url_kind` `post` / `profile`
