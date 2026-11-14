# analysis-engine Specification

## Purpose
Backend-local runtime behaviour of the analysis engine that implements the shared analysis-jobs-api and analysis-job-events contracts: job creation, applicability, content normalization, concurrency, DAG scheduling, cancellation, the settle barrier, the finalizer hook, the per-job event bus, job storage and eviction.

## Requirements

### Requirement: Job creation
The engine's create operation SHALL return the new Job with status `queued` and start it asynchronously. Every analyzer that is not `after_all_analyzers_settled` SHALL appear as an AnalyzerRun ordered by id, and the content type SHALL default to `text/plain`.

#### Scenario: Created job
- **WHEN** a job is created with content only
- **THEN** it is `queued` or `running`, lists every regular analyzer and has content type `text/plain`

### Requirement: Analyzer selection
With analyzers omitted all are selected; otherwise the named analyzers and their transitive dependencies are selected and the others are `skipped` with `not_selected`. Naming an unknown id SHALL fail with `UnknownAnalyzerError` (listing the ids) and naming an `after_all_analyzers_settled` analyzer with `NotSelectableError`; the two error types are distinct and no job is stored.

#### Scenario: Transitive selection
- **WHEN** `claims` depends on `entities` and a job selects only `claims`
- **THEN** `entities` and `claims` run and every other analyzer is `skipped` with `not_selected`

#### Scenario: Typed errors
- **WHEN** a job names `nope` or the finalizer id
- **THEN** creation fails with `UnknownAnalyzerError` or `NotSelectableError` respectively

### Requirement: Applicability
Applicability SHALL be evaluated once from `applies_to` against the Input, at job creation (for a job created with `scrape`: once its input is mapped): `content_types` must contain the input's media type (case-insensitive, parameters ignored), `requires_source_url` requires a non-empty `source.url` and `min_content_length` is compared with the character count of `normalized_content`. Non-matching analyzers SHALL be `skipped` with `not_applicable`.

#### Scenario: Inapplicable analyzer
- **WHEN** analyzer `htmlonly` applies only to `text/html` and the job content is `text/plain`
- **THEN** it is `skipped` with `not_applicable`

#### Scenario: Scrape job applicability after mapping
- **WHEN** analyzer `long` has `min_content_length: 100` and a scrape job's mapped content has 10 characters
- **THEN** `long` is `pending` while the job is `scraping` and is `skipped` with `not_applicable` after `scrape.succeeded`

### Requirement: Creation skips
Analyzers skipped at creation SHALL have the reason `disabled` (checked first), `not_selected` or `not_applicable`, SHALL never block the settle barrier and SHALL get their `analyzer.skipped` event right after `job.started`.

#### Scenario: Skips do not block
- **WHEN** a job has a disabled and an inapplicable analyzer besides a succeeding one
- **THEN** the job completes and the stream has `analyzer.skipped` for both right after `job.started`

### Requirement: Normalized content
`normalized_content` SHALL be computed once per job: for `text/html` the text content with tags removed (golang.org/x/net/html tokenizer; script and style bodies dropped, block-level tags separate lines), then Unicode NFC, `\r\n` and `\r` replaced by `\n`, and trimmed.

#### Scenario: HTML content
- **WHEN** the content is `<p>Hello <b>big</b> world</p>` with type `text/html`
- **THEN** `normalized_content` is `Hello big world`

#### Scenario: Plain content
- **WHEN** the content is `e` followed by U+0301 and a line break `\r\n` and the type is `text/plain`
- **THEN** `normalized_content` is the single character U+00E9

### Requirement: Per-job concurrency
At most `MAX_PARALLEL_ANALYZERS` (default 8) analyzers of one job SHALL execute at the same time; the others stay `pending`.

#### Scenario: Per-job limit
- **WHEN** 6 independent analyzers run with a limit of 2
- **THEN** never more than 2 execute concurrently and 2 do execute concurrently

### Requirement: Global job concurrency
At most `MAX_PARALLEL_JOBS` (default 16) jobs SHALL be running; further jobs SHALL stay `queued` in creation order until a slot is free, and a queued job can be cancelled.

#### Scenario: Queued job
- **WHEN** the global limit is 1 and a second job is created while the first runs
- **THEN** the second job has status `queued` and no `started_at` until the first finishes

### Requirement: DAG scheduling
An analyzer SHALL start only after all its dependencies `succeeded`; independent analyzers run concurrently. A settled AnalyzerRun SHALL never change, each produces exactly one settle event and `analyzer.started` precedes it for analyzers that ran. A failed analyzer SHALL NOT fail the job.

#### Scenario: Ordering
- **WHEN** `b` depends on `a`
- **THEN** `b` starts after the `analyzer.succeeded` event of `a`

### Requirement: Failure and skip propagation
An analyzer with a `failed` dependency SHALL be `skipped` with `dependency_failed`; one with a `skipped` dependency and no failed one SHALL be `skipped` with `dependency_skipped`; propagation is transitive.

#### Scenario: Propagation
- **WHEN** `a` fails, `b` depends on `a` and `c` depends on `b`
- **THEN** `b` is `skipped` with `dependency_failed`, `c` is `skipped` with `dependency_skipped` and the job ends `completed`

### Requirement: Analyzer timeout and context
Each execution SHALL run under a context with the analyzer's configured timeout, derived from the job context; exceeding the timeout SHALL fail the analyzer with `timeout`.

#### Scenario: Timeout
- **WHEN** an analyzer with timeout 50ms does not finish
- **THEN** it is `failed` with `timeout` and its dependents are `skipped` with `dependency_failed`

### Requirement: Settle barrier
After the settle event of the last AnalyzerRun the engine SHALL emit `analysis.ready_for_finalization` exactly once per non-cancelled job, also when analyzers settle concurrently, and set the job status `finalizing`.

#### Scenario: Concurrent settles
- **WHEN** 50 analyzers settle at the same moment, repeated 100 times
- **THEN** each job emits `analysis.ready_for_finalization` and `analysis.finalizing` once, after all 50 settle events, and the finalizer runs once

### Requirement: Finalizer hook
The engine SHALL expose a `Finalizer` interface `Run(ctx, FinalizationInput) (FinalAssessment as raw JSON, error)` invoked at most once per job after the barrier, under the timeout of the finalizer's registry definition. `FinalizationInput` carries a consistent clone of the job and the finalizer definition.

#### Scenario: Finalizer success
- **WHEN** the finalizer returns an object
- **THEN** the events `analysis.finalizing`, `analysis.finalized` and `job.completed` follow `analysis.ready_for_finalization` and the object is stored verbatim

### Requirement: Finalizer failures
The finalizer timeout SHALL bound the job even when the `Finalizer` ignores its context: on timeout or cancellation the engine SHALL stop waiting and drop a late result. A `*RunError` returned by the finalizer SHALL keep its code, a timeout SHALL map to `timeout`, any other error to `adapter_error` and a non-object result to `invalid_output`. AnalyzerRuns are never changed and the job still ends `completed`.

#### Scenario: Finalizer ignores its context
- **WHEN** a finalizer blocks past its 50ms timeout without honouring its context
- **THEN** finalization is `failed` with `timeout` within about a second, the late result is dropped and the global job slot is released

#### Scenario: Finalizer failure
- **WHEN** the finalizer returns an error
- **THEN** finalization is `failed` with `adapter_error`, analyzer results are unchanged and `job.completed` follows `analysis.finalization_failed`

### Requirement: Finalization skipped
Finalization SHALL be `skipped` at job creation with reason `disabled_by_request` when `options.finalize` is false, else `not_configured` when no `Finalizer` is registered or the registry has no enabled `after_all_analyzers_settled` analyzer. Its job SHALL reach `completed` with `job.completed` directly after `analysis.ready_for_finalization`. `Finalization.analyzer_id` SHALL be the enabled finalizer's id or null.

#### Scenario: No finalizer registered
- **WHEN** the engine has no `Finalizer`
- **THEN** the job completes with `finalization.status` `skipped` and reason `not_configured`

#### Scenario: Disabled by request
- **WHEN** a job is created with finalize false
- **THEN** finalization is `skipped` with reason `disabled_by_request`

### Requirement: Cancellation of analyzers
Cancelling an unfinished job SHALL cancel all running contexts and first emit the settle events of affected analyzers: pending ones become `skipped` with `cancelled`, running ones `failed` with `cancelled`, and settled runs keep their results. Late results of aborted executions SHALL be dropped.

#### Scenario: Cancel while running
- **WHEN** a job with one succeeded and one running analyzer is cancelled
- **THEN** the succeeded analyzer keeps its result, the running one is `failed` with `cancelled` and a pending dependent is `skipped` with `cancelled`

### Requirement: Cancellation of finalization and job
After the analyzer settle events, a running finalization SHALL end `failed` with `cancelled` (event `analysis.finalization_failed`), a pending one `skipped` with `cancelled` and an already skipped one keeps its reason; then `job.cancelled` is emitted and the status is `cancelled`. `analysis.ready_for_finalization` and `job.completed` SHALL NOT be emitted afterwards.

#### Scenario: Cancel during finalization
- **WHEN** the job is cancelled while the finalizer runs
- **THEN** the stream has `analysis.finalization_failed` with `cancelled` then `job.cancelled` and no `job.completed`

### Requirement: Cancel errors and queued jobs
Cancelling a finished job SHALL fail with `ErrJobFinished`, an unknown id with `ErrNotFound`. A job cancelled while queued SHALL emit `job.started`, its creation skips, the cancellation settles and `job.cancelled`.

#### Scenario: Cancel queued job
- **WHEN** a queued job is cancelled
- **THEN** its stream is `job.started`, `analyzer.skipped` for each analyzer and `job.cancelled`

#### Scenario: Cancel finished job
- **WHEN** a completed job is cancelled
- **THEN** the call fails with `ErrJobFinished`

### Requirement: Event sequence
The event bus SHALL assign `seq` per job starting at 1 and increasing by exactly 1 per event, and a job's stream SHALL end after its terminal event.

#### Scenario: Sequence
- **WHEN** two jobs emit events
- **THEN** each job's `seq` values are 1, 2, 3 and so on independently

### Requirement: Snapshot subscribe
`Subscribe` SHALL atomically return a `job.snapshot` event (data: the Job; `seq` the last emitted, 0 if none) followed by every later event in order without gaps or duplicates; for a job whose stream ended only the snapshot is delivered.

#### Scenario: Late subscriber
- **WHEN** a client subscribes after 3 of 5 analyzers succeeded
- **THEN** it receives a snapshot containing the 3 results with the last `seq` and then the later events

### Requirement: Slow subscribers
Each subscriber SHALL have a bounded buffer; when it is full the bus SHALL close that subscription and report `ErrSlowConsumer` instead of dropping events silently or blocking publishers.

#### Scenario: Slow subscriber
- **WHEN** a subscriber does not read while more events than its buffer holds are published
- **THEN** its channel is closed with `ErrSlowConsumer`, other subscribers are unaffected and the job proceeds

### Requirement: Job store and consistency
Job state SHALL live behind a `Store` interface (in-memory implementation). All state transitions and reads of one job SHALL be serialized by a per-job mutex and every event SHALL be emitted inside the critical section of the transition it describes, so that a read made after an event reflects at least that event.

#### Scenario: Read after event
- **WHEN** a client has received `analysis.finalizing` and reads the job
- **THEN** the job has status `finalizing` and `finalization.status` `running`

### Requirement: Retention
Finished jobs SHALL be evicted `JOB_RETENTION` (default 24h) after `finished_at`, together with their event stream; jobs that are not done SHALL never be evicted. Engine configuration is read from `MAX_PARALLEL_ANALYZERS`, `MAX_PARALLEL_JOBS` and `JOB_RETENTION`; invalid values are errors.

#### Scenario: Eviction
- **WHEN** a completed job is older than the retention and a running job exists
- **THEN** a sweep removes the completed job only and reads of it report not found

### Requirement: Engine test coverage
Unit tests SHALL cover every requirement of this change.

#### Scenario: Suite passes
- **WHEN** `go test ./... -race -count=1` runs in CI
- **THEN** it passes and covers registry validation, applicability, selection closure, DAG ordering, failure and skip propagation, timeout, cancellation while queued, running and finalizing, `http_sync` and `http_async` against `httptest` servers (success, 5xx, non-object body, schema violation, poll failure, `DELETE` on timeout), event sequence, snapshot and slow subscribers, and the exactly-once barrier under 50 concurrent settles repeated 100 times

### Requirement: Internal errors
A panic in the engine's execution goroutines or in the `Finalizer` SHALL end the job as `failed` with the terminal event `job.failed` instead of crashing the process; settled results are kept.

#### Scenario: Panicking finalizer
- **WHEN** the finalizer panics
- **THEN** the job ends `failed`, the last event is `job.failed` and settled AnalyzerRuns keep their results

### Requirement: Scraper client
The package `internal/scraper` SHALL be the engine's in-process adapter of the scraper core: it SHALL call `socialscraper.Scraper.Scrape` directly with the job's `url`, `max_posts` and `max_comments` (no HTTP request, no bearer token) and SHALL map the ScrapeResult to the job Input as before, keeping the ScrapeResult JSON in `source.metadata.scrape`.

#### Scenario: Unknown members ignored
- **WHEN** the core returns a ScrapeResult with members the mapping does not use, such as `bio` and `followers`
- **THEN** the scrape succeeds and `source.metadata.scrape` still contains them

#### Scenario: Post mapped to input
- **WHEN** the core returns a Mastodon post result with text `Vote no!` and a warning
- **THEN** the job input has content `Vote no!`, `source.platform` `mastodon` and the scrape has the warning

### Requirement: Scraper error mapping
The adapter SHALL pass the `code`, message (redacted) and `details` of a scraper error through, and SHALL give `scraper_unavailable` for `internal_error` (including a panic in a platform module) and for a result that is not a valid ScrapeResult (missing `platform` or `url_kind`, or `url_kind: post` without `post`), `timeout` when the scrape timeout ends the call and `cancelled` when the job is cancelled. Error messages SHALL NOT contain credentials.

#### Scenario: Error passed through
- **WHEN** the core fails with `account_private` and `details.platform` `instagram`
- **THEN** the adapter returns `code: account_private` with `details.platform: instagram`

#### Scenario: Panic
- **WHEN** a platform module panics
- **THEN** the adapter returns `scraper_unavailable` and the process keeps running

#### Scenario: Invalid body
- **WHEN** a module returns a result with `url_kind: post` and no `post`
- **THEN** the adapter returns `scraper_unavailable`

### Requirement: Scrape phase
For a job created with `scrape`, the engine SHALL run the scrape in the job's goroutine once the job holds a global job slot and before any analyzer is scheduled, under the job's cancellation context and `SCRAPER_TIMEOUT`. The job store SHALL keep the Scrape state on the job record and every scrape event SHALL be published through the job's event bus under the job lock, so REST reads and events agree. A scrape result that arrives after the job was cancelled SHALL be discarded.

#### Scenario: Cancel aborts the request
- **WHEN** a job is cancelled while its scrape is running
- **THEN** the scrape's context is cancelled and the job ends `cancelled` with `scrape.error.code: cancelled`

#### Scenario: Timeout
- **WHEN** the scrape does not finish within `SCRAPER_TIMEOUT`
- **THEN** the job ends `failed` with `scrape.error.code: timeout`

### Requirement: Scrape engine tests
Engine and adapter tests SHALL use an in-process scraper core with scripted platform modules and cover success for a post and a profile URL (input mapping), each error mapping including panic, invalid result, timeout and cancellation, `no_text_content` and the event order, and SHALL pass under the race detector.

#### Scenario: Suite passes
- **WHEN** `go test ./... -race -count=1` runs
- **THEN** the scrape engine and adapter tests pass

### Requirement: Adapter-reported skip
An adapter MAY end a run it was executing as skipped by returning a skip outcome with a reason. The engine SHALL settle the running run as `skipped` with that reason and `result: null`, emitting `analyzer.skipped` after `analyzer.started`; analyzers depending on it SHALL be skipped with `dependency_skipped`.

#### Scenario: Skip after start
- **WHEN** an adapter returns a skip outcome with reason `no_searchable_topics`
- **THEN** the events are `analyzer.started` then `analyzer.skipped` with that reason, the run has `result: null` and a dependent run is `skipped` with `dependency_skipped`

### Requirement: Availability at settle time
`availability` and `note` of an AnalyzerRun SHALL be computed by the single mapping function `job.Availability` when the run settles (succeeded, failed or skipped, including creation-time skips, dependency skips, cancellation and input_unavailable skips) and SHALL NOT be recomputed afterwards. Unsettled runs SHALL have both null.

#### Scenario: Same values in REST and events
- **WHEN** `f` fails with `adapter_error` and `df` (depending on `f`) is skipped with `dependency_failed`
- **THEN** both have `availability: unavailable` in `GET /api/v1/jobs/{id}` and identical values in their `analyzer.failed`/`analyzer.skipped` event `data.analyzer`

#### Scenario: Running run
- **WHEN** an analyzer is `running` or `pending`
- **THEN** `GET /api/v1/jobs/{id}` shows `availability: null` and `note: null`

### Requirement: Dependency-skipped resolution
Resolution of `dependency_skipped` SHALL use the job's own AnalyzerRuns and each run's `depends_on`, follow the first skipped dependency in `depends_on` order, stop after 8 steps and then yield `unavailable`. A run with no skipped dependency SHALL yield `unavailable`.

#### Scenario: Chain longer than 8
- **WHEN** a `dependency_skipped` run reaches its root skip only after 9 steps
- **THEN** it is `unavailable` with the unavailable note

#### Scenario: Root skip
- **WHEN** `source_info` is skipped with `dependency_skipped` and its first skipped dependency `news_search` has `no_searchable_topics`
- **THEN** `source_info` has `availability: not_applicable` and the same `note` as `news_search`
