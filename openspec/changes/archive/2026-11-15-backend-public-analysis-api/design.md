# Design: backend-public-analysis-api

Shared contract: `public-analysis-api` @ shared-specs 790c58b570d47ac1b857f333d59a12c4d6c53512.

## Handler
`submitPublic` takes a submit token, then reads the body with the existing `readJSON` (1 MiB, 413/400), rejects a
non-object with 400 `invalid_request`, collects member violations in the existing `fields` map (422), then
`missing_content`, then calls `Service.Create` once; `engine.ErrScrapeUnavailable` maps to 503. The request is
`engine.CreateRequest{Analyzers: nil, Finalize: &true, Private: !public}` with either `Input` (content type
`text/plain`, `source.metadata.public_api`) or `Scrape` with the ScrapeRequest defaults 20/50. `getPublic` takes a
get token, then reads through `Service.Get`.

## Mapping
Pure function of the stored Job. Summaries come from AnalyzerRuns with status `succeeded` only. Copied members are
taken as raw JSON (`pick`), so values pass through unchanged and absent members become null. A succeeded result that
does not decode contributes nothing rather than failing the request.

## Rate limit
A small `bucket` type (mutex, float tokens, injected clock) instead of generalising `internal/chat/limiter.go`,
whose per-job and in-flight rules do not apply; chat code is unchanged. Both buckets start full.

## share_url
`strings.TrimRight(PUBLIC_BASE_URL, "/") + "/jobs/" + url.PathEscape(id)`; never request headers.
