# Proposal: Propaganda map backend (junction-2026/project#59)

## Why
Shared change `propaganda-map-api` (shared-specs @ fa2a80df8674ccbbc6183931e95dbb7d7c8c5b85) defines map analyses (done, public
jobs with detected techniques and a known outlet country) and two read-only endpoints. This is the backend side.

## What Changes
- job-store: schema migration 3 adds per-job map values (country, outlet, technique set, eligibility flag) with an index; the values
  are derived once when a job becomes done, backfilled for stored done jobs at startup, and also derived by the Memory store.
- orchestrator-http-server: `GET /api/v1/map/countries` and `GET /api/v1/map/countries/{country_code}/analyses`.
- The map values come from `source_info.page_outlet`, else the outlet table (`internal/outlets`) matched against `input.source.url`.
