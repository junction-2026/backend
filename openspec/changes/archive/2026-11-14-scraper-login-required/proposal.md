# Proposal: scraper-login-required

Parent: junction-2026/project#19, child junction-2026/backend#60. Shared contract: shared-specs
`aec77e8a677c7da7239252a374a56d78088412db`, change `scrape-login-required` (capability `social-scraper-api`).

## Why
Anonymous Instagram answers 404 for existing accounts and mapped login walls to `rate_limited`; Threads serves a
page without data or with a login wall, and `threads.net` no longer serves it. Clients tell users that an existing
account does not exist or that waiting helps.

## What Changes
- `socialscraper.LoginRequired(platform, msg, missing...)`: code `login_required`, internal status 503,
  `details.platform`, `details.missing_settings` only when names are given.
- Instagram: login wall (redirect to `/accounts/login`, 401, error body with login markers, 2xx body passing
  `loginWallOK`) is `login_required`; only HTTP 429 is `rate_limited`. Without a session every "not found" (HTTP 404
  or an empty answer) is `login_required` with `missing_settings` `["INSTAGRAM_SESSION_FILE"]`; with a session it
  stays `account_not_found` / `post_not_found`.
- Threads: all requests and built URLs use `https://www.threads.com`; a 200 page without data is `login_required`
  when it shows a login form, a link to `/login` or login-required JSON flags, else `upstream_error`.
- README: `login_required` and the anonymous Instagram/Threads limitation.
- No Threads session setting: a plain cookie session is not verifiable offline, so it is documented as unsupported.
- No browser or browser automation, no new dependency.

## Impact
Local capability `scraper-meta-platforms` (modified requirements). Files: `internal/socialscraper/errors.go`,
`instagram/`, `threads/`, tests, `README.md`, `cmd/orchestrator/scraper_test.go` (Threads stub host).
