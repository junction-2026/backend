## Context

Shared design: shared-specs `web-page-scraping` `design.md` (D1–D7). This file records backend decisions only.

## Decisions

- **Providers behind one internal shape.** Both Oxylabs APIs map to `fetched{HTML, Markdown, FinalURL, TargetStatus}`;
  everything after that (extraction, fallback, login wall, cache) is provider-independent. The Web Scraper API
  answers `results[0].content`: when it looks like an HTML document it is HTML, otherwise Markdown (it was asked
  for `markdown: true`), so with that API the Markdown fallback is often the only extraction path.
- **Metadata is read by this repository, not by go-trafilatura.** The precedence rules (JSON-LD > Open Graph > HTML
  meta) differ from the library's, so `golang.org/x/net/html` collects the sources and trafilatura supplies only the
  main text. Paragraphs come from `ExtractResult.ContentNode`: `ContentText` has no paragraph breaks.
- **Retry budget is per scrape** (`session`): one 429/5xx retry in total, whichever fetch it hits, so the maximum is
  3 requests (unrendered, rendered, one retry). A target status ≥ 400 reported by the provider is not retried.
- **Per-request timeouts, not a client timeout.** An unrendered fetch is bounded by `SCRAPER_UPSTREAM_TIMEOUT_SECONDS`,
  a rendered one by `WEB_TIMEOUT_SECONDS` (default `max(upstream, 150 s)`); the job-level default is 30 + 150 + 150 s
  rounded up to 360 s.
- **No redirects to the provider's host are followed**, so the `Authorization` header never reaches another host.
- **Fallback in the core.** `Scraper.Scrape` turns an `unsupported_url` of the Mastodon module into a web target;
  modules stay unaware of each other.
- **Username null for pages** through `Result.MarshalJSON`; Bluesky and Mastodon results are unchanged.
- **Legacy IPv4 spellings** (decimal, octal, hex) are refused with the local hosts: a host whose last label starts
  with a digit and is not a valid IP address is not a real hostname.
- **Cache:** map with a mutex, bounded to 500 entries, expired entries dropped first, then the oldest.
  shortcut: no single-flight, two concurrent scrapes of one URL both call the provider.

## Risks / Trade-offs

- The Oxylabs response member names (`results[0].html`, `markdown`, `content`, `status_code`, `url`) follow the
  approved task text and are not verified against the live service; tests use a stub only.
- go-trafilatura brings a WebAssembly regex engine (`wazero`, pure Go): `CGO_ENABLED=0` builds still work.
