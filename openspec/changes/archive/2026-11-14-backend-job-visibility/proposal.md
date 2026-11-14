# Proposal: job visibility (optional `public` flag)

## Why

Parent feature junction-2026/project#53: a user can create an analysis job that is not listed in the history
(`GET /api/v1/jobs`) but stays reachable by id. The shared change `job-visibility` defines the API; this change
implements it in the backend.

## What Changes

- `POST /api/v1/jobs` reads an optional strict boolean `public` (default true); invalid values give 422 `validation_failed` with `details.fields.public`.
- The Job carries an immutable `public` member, set at creation, in every Job representation.
- The SQLite jobs table gains a NOT NULL `public` column (default 1) by a migration, plus indexes; `Page` filters `public = 1` in SQL before ordering and the limit. The memory store filters the same way.
- Stored Job JSON without `public` decodes as public.

## Shared specs

Pinned `junction-2026/shared-specs@9a26edb52efa7e1ee9a46eea7721be73f67b50b7`, change `job-visibility`
(capability `analysis-jobs-api`). No shared spec is edited here.

## Capabilities

### Modified Capabilities
- `job-store`: column, migration, index, filtered paging, legacy decoding
- `orchestrator-http-server`: create-body parsing of `public`
- `analysis-engine`: the Job `public` field
