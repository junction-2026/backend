# Proposal: Ask VEIL chat (backend)

## Why
Parent feature junction-2026/project#57, child issue junction-2026/backend#135. Shared change `analysis-chat-api`
(shared-specs @ 5f4e9e4789a043c2ee8620f54fc3da58cea1cb86) defines `POST /api/v1/jobs/{id}/chat`. The backend
implements it by calling a self-hosted OpenAI-compatible agent (Hermes) and presenting it as VEIL.

## What Changes
- New package `internal/chat` (upstream client, embedded persona prompt, grounding builder, history window, limiter).
- Route `POST /api/v1/jobs/{id}/chat` and handler in `internal/httpapi` (read-only on the job store).
- Configuration `CHAT_BASE_URL`, `CHAT_MODEL`, `CHAT_API_KEY`/`CHAT_API_KEY_FILE`, `CHAT_TIMEOUT`,
  `CHAT_RATE_PER_MINUTE`, `CHAT_MAX_CONCURRENT` in `cmd/orchestrator`; the key joins the log redactor.
- README: configuration table, container networking to a loopback-bound agent, key rotation.
- New local capability `analysis-chat` (runtime behaviour only; the HTTP contract stays in the shared spec).

## Non-goals
Streaming, stored chat history, changes to the agent, any change to the Job object or its events.

## Security
The Hermes API key was posted in Discord and must be rotated. It is only a deployment secret (env or file), never
committed, logged or returned.
