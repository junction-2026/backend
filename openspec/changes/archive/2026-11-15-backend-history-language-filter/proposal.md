# Proposal: History language filter (junction-2026/project#80, junction-2026/backend#161)

## Why
Shared change `history-language-filter` (shared-specs @ 4d2ab1830d2d2c7f7e4ac1592e17f2dc33307aec; capability
`analysis-jobs-api`) adds the optional `language` filter to `GET /api/v1/jobs` and the JobSummary member `language`. The
job language is `$job.source_language` (`adapter.JobSourceLanguage`). Filtering the persisted history by decoding every
stored job would not scale, so the backend stores the derived value, indexed.

## What Changes
- job-store: each stored job holds its derived job language, computed whenever the job is written; schema migration 5
  adds the nullable `language` column and the index `(public, language, created_at DESC, id)` and backfills every
  existing row in the same transaction, in batches; `Store.Page` takes a language filter (code, unknown or none) applied
  in SQL (Memory store: on the language recorded at save) with status, q and public-only before cursor and limit.
- analysis-engine: the persisted language follows the translation run through the existing write points (every state
  change and the interrupted-job recovery save the job); null until the run succeeded, unchanged afterwards.
- orchestrator-http-server: `GET /api/v1/jobs` parses and validates `language`; JobSummary always carries `language`.
- The job-language rule moves to `job.Job.SourceLanguage` (package `job`, which `store` may import);
  `adapter.JobSourceLanguage` delegates to it, so there is still one definition.

## Impact
`internal/job/language.go` (new), `internal/adapter`, `internal/store`, `internal/engine` (Page signature),
`internal/httpapi/jobs.go`. One appended migration; released migrations unchanged. No new configuration or endpoint.
