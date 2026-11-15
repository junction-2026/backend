# Proposal: AI country association (junction-2026/project#75, junction-2026/backend#155)

## Why
Shared change `ai-country-association` (shared-specs @ 24b7969236d03059dd6447481c27afd541fcac7e; capabilities
`country-association`, `propaganda-map-api`, `analysis-jobs-api`) places analyses of pasted text, unknown outlets and
content about other countries on the Explore propaganda map. The backend so far only used the outlet country and its local
specs forbid inferring a country from text.

## What Changes
- analysis-engine: an LLM country association step runs once per job with detected propaganda at the settle barrier,
  concurrently with finalization; the job completes once both settled. Skips, failures, cancellation and restart
  interruption settle `Job.country_association` without touching status, runs, risk or finalization. A background backfill
  runs the step for stored done public jobs whose association is missing or failed.
- New configuration `COUNTRY_ASSOCIATION_*` (OpenAI-compatible, not configured without a model) and `COUNTRY_BACKFILL_BATCH`;
  an ISO 3166-1 alpha-2 table ships as a data file loaded at start.
- job-store: `Job.country_association` is persisted in the job JSON; schema migration 4 stores the map countries of a job in
  a child table `job_map_countries` (per-country listing index) and as JSON; the existing single map country becomes the
  outlet-basis country; per-job backfill attempts; conditional backfill update.
- orchestrator-http-server: MapAnalysis gains `countries`, `country_code`/`country` are those of the requested list,
  `outlet` may be null, `source.url` may be `""`; the top-level `analyses` counts distinct map analyses.
- Removed: the local rule that the map country is never inferred from a TLD, language or text.

## Impact
`internal/country` (new), `internal/adapter/country.go` (new), `internal/engine`, `internal/job`, `internal/store`,
`internal/httpapi/map.go`, `cmd/orchestrator`. No new event type. Deployments without `COUNTRY_ASSOCIATION_MODEL` keep
today's map.
