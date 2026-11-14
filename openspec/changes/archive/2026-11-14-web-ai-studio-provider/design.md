# Design

- One file, `aistudio.go`; the HTML flow in `web.go` is untouched except that `Scrape` branches to `scrapeAIStudio` after the cache lookup and `host`/`domain` computing moved into `hostDomain`. Cache key and cache are shared.
- A run (start, poll, data) runs under one `context.WithTimeout(WEB_TIMEOUT_SECONDS)` derived from the job context, so the deadline or a cancellation ends polling at once. Poll waits start at 1 s and grow by 1.5x to 3 s; both are fields of the module for tests.
- Polling status: `processing`, `pending`, `queued`, `running` keep polling, `completed` fetches data, anything else is a failed run (the poll answer's `error_code` is carried).
- `provider_error_code` is only kept when it is a short token (`[A-Za-z0-9_.:-]{1,64}`) that does not contain the API key, so a provider echoing the key cannot leak it.
- A canonical URL longer than the 500-character cap is dropped instead of truncated (a cut URL points elsewhere).
- When the JSON run succeeded with a short body and the Markdown run yields no more text, the short JSON body is the result (like short HTML pages); with no text at all the scrape fails with `no_text_content`.
- Logging: the module logs nothing; `OXYLABS_AI_STUDIO_API_KEY` is added to the redactor's secret list for defence in depth.
