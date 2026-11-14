# Design: Facebook and X modules

## Decisions

1. **Facebook = the page's own GraphQL.** The module opens `https://www.facebook.com/<path>` (rebuilt from the
   recognized ids, so a scraped URL never chooses the host), listens to `response` events whose URL contains
   `/api/graphql`, and parses the JSON documents (batched answers are one document per line). Nodes are found by
   `__typename` anywhere in the documents (`Page`/`User` profile, `Story` post, `Comment`). Profile pages scroll up
   to 12 rounds until `max_posts` stories are present; post pages take the comments the page loads by itself.
2. **Personal profiles.** `profile.php` URLs are refused by the recognizer. A vanity URL that resolves to a `User`
   node (matching the URL slug) is refused after loading with `unsupported_account_type` / `personal_profile`.
   Commenter `User` nodes are never mistaken for the profile: the slug match or a `Page` node wins.
3. **Time budget and cleanup.** Page work uses 80% of `ScrapeRequest.timeout_s` (goto timeout and scroll deadline);
   the core's `asyncio.timeout` still bounds the whole scrape. The browser is closed in a `finally`, so success, a
   typed error, a Playwright timeout and task cancellation all close it. Chromium starts with `--no-sandbox` (the
   container user is unprivileged and has no user namespaces) and `--disable-dev-shm-usage`.
4. **X = twscrape.** One pool account `junction` is created from the two cookies. `user_by_login`,
   `tweet_details`, `user_tweets` (own tweets only, retweets skipped, newest first) and `tweet_replies` (direct
   replies, sorted oldest first) fill the result. Failure of the profile or post is a typed error; failure of
   history or replies is a warning. `NoAccountError` is `rate_limited` while the pool has an active account (locked)
   and `upstream_error` when none is active (cookies rejected).
5. **Pool built on first use, not at import.** The module interface has no startup hook and the core is not changed
   by this task, so the pool is created on the first X scrape from the environment and cached. Missing variables
   answer `platform_not_configured` before any pool exists, so a deployment without X credentials never touches
   twscrape. The sqlite file holds the cookies: it is placed in a private (`0700`) temp directory, or at
   `SCRAPER_X_POOL_DB` for a volume; never in the repository.
6. **No credential leak.** Messages are fixed strings; exception text is never copied into a response or log
   line (only its class name). twscrape's own loguru output is limited to errors.
7. **Offline tests.** Facebook tests replace `async_playwright` with a fake that replays recorded GraphQL
   documents; X tests replace the API object with a fake serving twscrape models built from recorded fixtures, plus
   one test that builds the real pool in a temp directory (sqlite only). An autouse fixture makes any test that
   forgets to fake either module fail instead of starting chromium or reaching X.

## Risks

- Facebook GraphQL node shapes are modelled on the documented assumptions in `facebook.py`; Facebook changes them
  without notice, and fixtures cannot detect drift. Missing members degrade to `null` plus `partial_parse`.
- A single X account is a throughput and ban risk; concurrent X scrapes queue for up to 5 seconds, then answer `rate_limited`.
- The history scan reads at most 3x `max_posts` timeline entries, so retweet-heavy accounts may return fewer posts.
