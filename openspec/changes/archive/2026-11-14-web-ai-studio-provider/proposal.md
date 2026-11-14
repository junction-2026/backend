# Web page scraper: Oxylabs AI Studio provider

Parent: junction-2026/project#34, child junction-2026/backend#105. Shared contract: shared-specs change `oxylabs-ai-studio-provider` @ 970fc98f0479ca25cbe2725cccc5ebe5f4458c98 (`social-scraper-api` delta).

## Why
The team's Oxylabs plan is AI Studio, whose key the Web API rejects. The backend needs a third page provider, `OXYLABS_API=ai_studio`.

## What Changes
- `OXYLABS_API` accepts `ai_studio`; new settings `OXYLABS_AI_STUDIO_API_KEY` and `OXYLABS_AI_STUDIO_URL`.
- New provider in `internal/socialscraper/web/aistudio.go`: run lifecycle (start, poll, data), JSON schema, sanitizing of model output, mapping, Markdown fallback run, errors.
- README settings table documents the three values.

`web_api` stays the default; `web_api` and `web_scraper_api` behaviour and tests are unchanged.

## Impact
Backend only; no shared contract is defined here (see the shared change for `provider`, `extraction`, `oxylabs_requests`, `details.provider_error_code`).
