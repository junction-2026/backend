## Context
Shared design D1 (shared-specs `6d1e0c0`, change `remove-closed-platform-scraping`): only `bsky.app` stays
excluded from Mastodon recognition. D2: `login_required` is removed, `platform_not_configured` stays generic.

## Decisions
- `Recognize` keeps its switch with one `bsky.app` case; the default branch is Mastodon recognition, guarded by
  `listedHosts` (now only `bsky.app`) and the hostname check. A Mastodon-shaped path on any other host costs one
  outbound request bounded by the existing Mastodon host-safety rules (no redirects, https, same host).
- `NewRedactor` loses its `readFile` parameter: the only file it read was the removed session file. Keeping an
  unused parameter would invite reading files again.
- `newScraper` loses its `getenv` parameter: no module reads the environment any more.
- `SCRAPER_TOKEN` stays in the redactor's secret list: it is an obsolete variable that may still be set and is
  only ever named, never logged.

## Risks
- Jobs created before the change keep recorded platform values and `login_required` codes; clients read them
  under the shared tolerant-reader rule. No data migration.
