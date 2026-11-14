# Design

## Facebook

`facebook.Module{Launch}` starts one browser per scrape through `Launch(ctx) (Browser, error)`. `Browser` is
`Navigate(url) (status, error)`, `Scroll() error`, `Bodies() [][]byte`, `Text() string`, `Close()`. `Scroll` is
beyond the four calls named in the plan: profile history needs scrolling, and a fake that emits one batch per
scroll keeps that logic testable offline. The real implementation (`chrome.go`) is a chromedp exec allocator with
`--headless --no-sandbox --disable-dev-shm-usage`, the chromium found on PATH; it listens to CDP
`Network.responseReceived` / `loadingFinished`, keeps the bodies of responses whose URL is
`https://www.facebook.com/api/graphql/...` (bodies over 16 MiB are skipped), and records the status of the main
document. `launchChrome` panics when `testing.Testing()` is true, so a test that forgot to install a fake fails
instead of starting chromium. `Scrape` calls `defer b.Close()` right after a successful launch, so success, error,
panic and context cancellation all close the browser (chromedp waits for the process to exit).

Parsing is the walker of the earlier implementation, ported: every JSON document in every body (a body may be one
JSON value or one document per line, with an optional `for (;;);` prefix) is walked for `Page`, `User`, `Story`
and `Comment` nodes. Scrolling stops at 80% of the scrape deadline.

## X

`x.Module{HTTP, Getenv, Now}` reads the two cookies from the environment on every scrape, so a missing one answers
503 before any request. Requests are GETs of `https://x.com/i/api/graphql/<queryId>/<op>?variables=&features=`
with `Authorization: Bearer <public web bearer>`, `Cookie: auth_token=...; ct0=...` and `x-csrf-token: <ct0>`;
redirects are not followed. Query ids, features and the bearer are constants in `endpoints.go` because X rotates
them; updating that one file is the maintenance path. Responses are decoded into small structs that accept both the
`core` and the older `legacy` placement of user names. Pinned-tweet entries are ignored (the tweet appears at its
own position); user tweets are paged by bottom cursor up to 10 requests; replies come from the `TweetDetail`
conversation modules, filtered to `in_reply_to_status_id_str == post id`, and further pages (the "show more"
cursor) are requested only while fewer than `max_comments` replies are known.

Errors from upstream are never copied: every `*ss.Error` has a fixed message, and unexpected failures log only
`%T` of the Go error through `slog`.

## Not verified

The query ids, feature flags and response shapes follow the public web client as known when the change was
written; nothing in this change contacts X or Facebook. They can only be confirmed with real credentials.
