# Design: scraper service core

## Decisions

1. **App factory.** `uvicorn app.main:create_app --factory`. `create_app()` reads settings and exits with a
   clear message when `SCRAPER_TOKEN` is unset, so a misconfigured container stops at start instead of serving 401s.
2. **Auth and body handling in the route.** The route checks the bearer token first and only then reads the body
   itself (400 for broken JSON, pydantic for 422 `details.fields`), so FastAPI's default 422/`detail` format and
   body-before-auth ordering never apply. HTTP errors of unknown paths use the same envelope.
3. **Module interface** (`app/platforms/base.py`): `NAME`, `recognize(NormalizedUrl) -> Recognized | None`,
   `async scrape(ScrapeRequest) -> ScrapeResult`. `app/registry.py` holds the only module list. Mastodon is last
   because it matches any host that is not another listed platform (`urls.LISTED_HOSTS`).
4. **Platforms without a module** are `Pending` entries in the registry (`platforms/pending.py`): they carry
   the real recognizer for the URL grammar and answer `platform_not_configured` (503, `details.missing_settings: []`)
   because the shared list of error codes has no "module missing" code and 500/502 would misreport a
   deployment state. Follow-up tasks replace the entry with their module.
5. **Time budget.** `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` bounds a whole scrape (`asyncio.timeout`, 504
   `upstream_timeout`) and each HTTP request. Failure of secondary data (history pages, comments) becomes a
   warning, failure of the requested profile or post becomes a typed error.
6. **Bluesky.** atproto SDK `AsyncClient` against `https://public.api.bsky.app`, no login. Post and comment ids
   are AT URIs (unique across authors, so `in_reply_to_id` is unambiguous). Reposts are skipped in history.
   `!no-unauthenticated` label maps to `account_private`.
7. **Mastodon.** `httpx` against `https://<host>/api/v1/...`; the host comes from the URL but IP literals and
   dotless names are never recognized, redirects are not followed and `Link: rel=next` is followed only on the
   same host. A non-JSON answer means the host is not Mastodon: `unsupported_url`. `verified` is always null
   (Mastodon states no verification flag).
8. **Secrets.** Only environment variables. The log filter redacts `Authorization` values and the values of
   secret-named environment variables; tracebacks are logged only through that filter and never returned.

## Risks

- Fixtures are synthetic recordings of the documented API shapes; they cannot detect upstream drift.
- A Mastodon host is contacted on any `/@user` URL of an unknown host (authenticated callers only); DNS-level
  SSRF protection (blocking private resolution results) is not implemented.
