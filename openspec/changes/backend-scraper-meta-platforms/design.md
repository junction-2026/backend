# Design: Instagram and Threads modules

## Decisions

1. **instaloader in a worker thread.** instaloader is blocking (requests). `scrape()` runs the whole scrape with
   `asyncio.to_thread`; the core's `asyncio.timeout` still answers 504. The loader is created per scrape with
   `sleep=False`, `max_connection_attempts=1`, `request_timeout=SCRAPER_UPSTREAM_TIMEOUT_SECONDS` and a rate
   controller that never waits: instaloader's own back-off can sleep for minutes, a service request must answer.
2. **Session.** `INSTAGRAM_SESSION_FILE` (an instaloader session file named `session-<username>`) is loaded when set.
   A file that cannot be loaded means an anonymous scrape; neither the path nor content is logged (the core log
   filter also redacts the value because the name contains `SESSION`).
3. **Failure mapping** walks the exception cause chain, because `get_json` wraps the final failure in a
   `ConnectionException`: timeout → `upstream_timeout`; `TooManyRequestsException`, `LoginRequiredException` or an
   HTTP 429 in the text → `rate_limited` (instaloader exposes no Retry-After, so none is sent); not found →
   `account_not_found`/`post_not_found`; private profile (`is_private`) → `account_private`; everything else
   `upstream_error`. A media query that answers without an item is `post_not_found`.
4. **Partial data.** Anonymous comments need a login: the result keeps `comments: []` and a `comments_unavailable`
   warning. A throttle or error while walking the history keeps the posts fetched so far
   (`history_truncated`, or `history_unavailable` when none). The history loop stops at 80 % of the timeout.
   Anonymous history costs one request per post (the timeline lists only shortcodes).
5. **Unknown is null.** Profile members come through `_opt()` (missing key → null, plus one `partial_parse`
   warning for missing counters). Post counters are read from the raw media node, because instaloader fills
   a missing Polaris `like_count` with 0; hidden likes (`like_and_view_counts_disabled`) are null. `reposts` is
   always null on Instagram. Post `id` is the shortcode, the identifier the URL carries.
6. **Threads.** `GET https://www.threads.com/@user[/post/<code>]` (no redirects) with a browser User-Agent. The
   `<script type="application/json">` blocks are walked for objects that look like a user (`username` plus
   profile members) and like a post (`pk`, `code`, `user`, `caption`/`taken_at`) instead of following a fixed
   path, because Relay nesting changes. A post object without `code`/`pk` is skipped with a `partial_parse` warning;
   a page without any recognizable profile or post data is `upstream_error`. `is_private: true` is
   `account_private`. Followers, bio and verification come from the profile page; `following` and `post_count`
   are not exposed by Threads and stay null. Comments of a post URL are the other posts embedded in the page
   that are not older than the post; `in_reply_to_id` is null because the page does not name it reliably. The
   author's history comes from a second request for the profile page; its failure is a `history_unavailable` warning.
7. **Tests.** Instagram: real instaloader `Profile`/`Post`/`PostComment` objects parse recorded JSON; only the
   context's HTTP methods are replaced. Threads: respx serves recorded HTML pages. A socket guard in
   `conftest.py` fails any test that tries to connect.

## Risks

- No live recording was possible in the build environment: the fixtures follow the data shapes documented in
  instaloader 4.16's parsers and in public Threads page structure, so they cannot detect upstream drift.
- Instagram throttles anonymous clients hard; expect frequent `rate_limited` without a session.
- Threads serves only the posts embedded in the first page (no pagination), so `posts` can hold fewer than
  `max_posts` without a warning.
