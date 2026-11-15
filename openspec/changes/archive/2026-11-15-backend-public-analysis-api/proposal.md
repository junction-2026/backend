# Proposal: backend-public-analysis-api

## Why
Parent feature junction-2026/project#100 (child junction-2026/backend#167) adds an unauthenticated public API for
platforms and outlets. The shared contract is capability `public-analysis-api` (shared-specs change
`public-analysis-api` @ 790c58b570d47ac1b857f333d59a12c4d6c53512). This change holds the backend-local parts:
configuration, the rate limiter, how the handler uses the engine, logging, tests and integrator docs.

## What Changes
- New `internal/httpapi/public.go`: `POST /api/v1/public/analyses` and `GET /api/v1/public/analyses/{id}`,
  registered in `NewHandler`; composition into one `input.content`; Job to PublicAnalysis mapping.
- Two process-wide token buckets (submit, get) in `internal/httpapi`; the chat limiter is not touched.
- Configuration `PUBLIC_API_SUBMIT_PER_MINUTE`, `PUBLIC_API_GET_PER_MINUTE`, `PUBLIC_BASE_URL` in `cmd/orchestrator`.
- `docs/public-api.md` documents both endpoints for integrators.

## Impact
- Code: `internal/httpapi/{httpapi.go,public.go,public_test.go}`, `cmd/orchestrator/{config.go,main.go,public_test.go}`,
  `docs/public-api.md`.
- No change to `/api/v1/jobs`, the Job object, the engine, the store or the chat limiter.
