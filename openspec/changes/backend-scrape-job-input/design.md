# Design: scrape phase in the orchestrator

## Decisions
1. **`Job.input` becomes `*job.Input`** (null for scrape jobs until the scrape succeeded). Adapters and the
   finalizer only run after input is set, so they dereference it unchanged. `CreateRequest.Input` stays a value;
   `CreateRequest.Scrape` (non-nil) selects a scrape job.
2. **Scrape runs inside the job's slot.** `run.main` emits `job.started` (status `scraping`) and the creation skips
   (`disabled`, `not_selected`) as for text jobs, then `scrape.started`, and starts the scrape in a goroutine with
   `context.WithTimeout(job ctx, SCRAPER_TIMEOUT)`. Its result is applied under the job lock; a result that arrives
   after cancellation is dropped. Creation-skip events precede `scrape.started` because a job cancelled while
   `queued` also emits them (via `beginLocked`) but must not emit `scrape.started` for a scrape that never ran.
3. **Success**: Scrape `succeeded`, `Job.input` set (content type `text/plain`, normalized content computed as for
   text jobs), `scrape.succeeded`, status `running`, `not_applicable` skips, then normal DAG scheduling. Every later
   rule (barrier, finalizer, insufficient-evidence short-circuit) is the text-job code path.
4. **Failure**: Scrape `failed`, `scrape.failed`, pending runs `skipped`/`input_unavailable`, pending finalization
   `skipped`/`input_unavailable`, `job.failed`; no barrier event, the finalizer is never called.
5. **Cancellation**: `abortLocked` settles an unsettled Scrape as `failed`/`cancelled` (emitting `scrape.failed`)
   before the existing analyzer and finalization handling, so `scrape.failed` precedes the analyzer skips.
6. **Client mapping** lives in `internal/scraper` so the engine only sees `(Result, *job.ScrapeError)`. Our own error
   messages are fixed strings; the scraper's pass-through message has the scraper URL and token removed.
7. **Unconfigured scraper**: `engine.Create` returns `ErrScrapeUnavailable` before creating anything; the HTTP layer
   answers 503 `scrape_unavailable` after body validation.
