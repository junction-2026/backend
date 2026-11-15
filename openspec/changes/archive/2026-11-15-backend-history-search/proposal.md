# Proposal: History search and job excerpts, backend (junction-2026/project#67, junction-2026/backend#149)

## Why
Shared change `history-search-and-excerpt` (shared-specs @ d4745c605a5b00999884130a9b08823a04c2c4e9, `analysis-jobs-api`
delta) adds the search parameter `q` to `GET /api/v1/jobs` and the JobSummary member `excerpt`. This is the backend side.

## What Changes
- `store.Store.Page` takes a search query; `store.Matches` is the one matching rule (Unicode lowercase, literal substring
  over `id`, `input.content`, `scrape.url`) used by the Memory and the SQLite store.
- SQLite: with a search, the listing query drops `LIMIT` and scans rows in order, matching in Go, until `limit` matches or
  the rows end. No migration, column or index.
- `engine.Engine.Page` passes the query through.
- `GET /api/v1/jobs` trims `q`, rejects more than 200 code points with 400 `invalid_request`; `summarize` sets `excerpt`.

## Contracts
Shared: `history-search-and-excerpt` / `analysis-jobs-api` (List jobs, Job list search, Job list search matching, scope,
paging, JobSummary excerpt) at the pinned SHA. Nothing is duplicated here; the local specs cover store behaviour, parsing and tests.

## Impact
`internal/httpapi/jobs.go`, `internal/httpapi/httpapi.go`, `internal/store/store.go`, `internal/store/sqlite.go`,
`internal/engine/engine.go` and their tests.
