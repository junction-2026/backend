# Design: Instagram and Threads in Go

## Decisions

1. **Instagram data flow.** Profile: `GET www.instagram.com/api/v1/users/web_profile_info/?username=<u>`
   (`data.user`: counters as `edge_followed_by.count`, `edge_follow.count`, `edge_owner_to_timeline_media.count`).
   History: `GET i.instagram.com/api/v1/feed/user/<user id>/?count=<n>[&max_id=<cursor>]`, paged while
   `more_available` and fewer than `max_posts` posts were read. Post URL: the shortcode is decoded (base-64
   alphabet `A-Za-z0-9-_`, first 11 characters) to the media id and `GET i.instagram.com/api/v1/media/<id>/info/`
   names the post and its owner; the owner's profile and history are then read as for a profile URL. Feed items and
   media info items share one shape (`code`, `pk`, `taken_at`, `caption.text`, `like_count`, `comment_count`,
   `like_and_view_counts_disabled`, `user`). Endpoint paths, the `X-IG-App-ID` web header and the User-Agent live
   in `endpoints.go`.
2. **Null, not zero.** Counters are `*int` in the decoded structs, so an absent key stays null; a post with
   `like_and_view_counts_disabled` has `likes: null` whatever `like_count` says.
3. **Comments.** Only requested with a session (`/api/v1/media/<id>/comments/`, paged through `next_min_id` /
   `next_max_id` up to 5 pages, replies from `preview_child_comments` or `/comments/<pk>/child_comments/`). They are
   sorted oldest first over everything read and cut to `max_comments`: with more comments than 5 pages hold the
   oldest ones may be missing (accepted limit). Anonymously, and when the comments request fails, the answer is 200
   with `comments_unavailable`.
4. **Session.** `INSTAGRAM_SESSION_FILE` is read once when the module is built (registration). A missing,
   unreadable, malformed file or one without `sessionid` (or with a value that cannot be a cookie value) logs the
   fixed line "instagram session file could not be loaded; scraping anonymously" and is otherwise ignored; the path
   and values are never an argument of a log call, error or message. The module gets its logger from the caller
   (`ss.DefaultLogger()`, which also redacts the file's values).
5. **No redirects.** Both modules copy the injected client and refuse redirects: Instagram's login redirect is the
   "login wall" and no other host is contacted. A redirect to a `/accounts/login` location, a body naming
   `login_required` / `require_login`, or HTML where JSON was expected is a login wall (429 `rate_limited`).
6. **Threads.** The page is tokenized with `golang.org/x/net/html`; the text of every
   `<script type="application/json">` element is decoded (`UseNumber`) and walked iteratively (maps in sorted key
   order, so the result is deterministic) for user-like objects (`username` plus a profile hint, no `code`) and
   post-like objects (`user` object, `caption` or `taken_at`; usable only with `pk` and `code`). The semantics are
   those of the earlier implementation: user candidates are merged richest first, posts are keyed by `pk`, replies
   on a post page are the other posts of the page not older than the post, `following` and `post_count` are null.
7. **Registration.** `cmd/scraper/platforms_meta.go` appends to `platformInits`; the registering function takes the
   registry, client, session file path and logger so a test can route the client to fixtures.
8. **Tests.** Routed fixtures through `scrapertest`; module tests per package, plus a server-level test in
   `cmd/scraper` (the reel and Threads profile URLs of the shared spec, the sentinel session value and path).
   `cmd/scraper/main_test.go` loses Instagram and Threads from the "not registered" list.

## Risks

- Instagram changes its web endpoints and anti-bot behavior often; every unexpected answer degrades to
  `upstream_error` / `rate_limited` rather than a 500. Pinned posts on top of a feed may be older than the posts
  after them; posts are sorted newest first only within what was read.
