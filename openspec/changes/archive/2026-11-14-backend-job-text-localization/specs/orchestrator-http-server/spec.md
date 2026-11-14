## ADDED Requirements

### Requirement: Job localization route
The server SHALL serve `GET /api/v1/jobs/{id}/localization` as defined by the shared `job-text-localization` capability (shared-specs @ 07b1fce6c316aa2ddf926ffab423dd721fb5b9b4). Other methods SHALL get 405 `method_not_allowed` with `Allow: GET`.

#### Scenario: Other method
- **WHEN** a client calls `POST /api/v1/jobs/j1/localization`
- **THEN** the status is 405 with `Allow: GET`

### Requirement: Job localization errors
The handler SHALL answer, in the api-conventions error envelope, a missing or blank `language` with 400 `invalid_request`, a `language` whose lower-case primary subtag is not supported with 400 `unsupported_language` and `error.details.supported` listing `en`, `fr`, `ar`, `de` and `fi`, an unknown job with 404 `not_found` and a job that is not done with 409 `job_not_finished`.

#### Scenario: Unsupported language
- **WHEN** a client calls `GET /api/v1/jobs/j1/localization?language=xx`
- **THEN** the status is 400, `error.code` is `unsupported_language` and `error.details.supported` is `["en","fr","ar","de","fi"]`

#### Scenario: Missing language
- **WHEN** a client calls `GET /api/v1/jobs/j1/localization` without `language`
- **THEN** the status is 400 with `error.code` `invalid_request`

#### Scenario: Region subtag
- **WHEN** a client calls it with `language=DE-at` for a done job
- **THEN** the status is 200 and `language` is `de`

### Requirement: Localization reads the job only
The localization handler SHALL load the job through the existing job service (`Service.Get`, backed by the configured job store, SQLite with `JOB_STORE=sqlite`), so persisted and recovered done jobs are served like live ones. It SHALL NOT write to the job store, change the job or publish an event.

#### Scenario: Persisted job
- **WHEN** a done job is only in the SQLite store (not in the working set) and is localized
- **THEN** the status is 200 and the stored job and `GET /api/v1/jobs/{id}` are unchanged afterwards

#### Scenario: No events
- **WHEN** a subscriber listens on a job's event stream while the job is localized in three languages
- **THEN** it receives no event

### Requirement: Localization timeout setting
`LOCALIZATION_TIMEOUT` (Go duration, default `20s`) SHALL bound each translation provider call of the localization. A value that is not a positive duration SHALL be a configuration error naming the variable. On timeout the texts of that call SHALL be `unavailable`.

#### Scenario: Invalid timeout
- **WHEN** `LOCALIZATION_TIMEOUT=-1s`
- **THEN** the process exits non-zero and the message names `LOCALIZATION_TIMEOUT`

#### Scenario: Slow provider
- **WHEN** the provider does not answer within the timeout
- **THEN** the response is 200 and every text that needed a translation has `status: unavailable` and `text: null`

### Requirement: Localization cache
The server SHALL keep translated texts in memory per (job id, language), bounded to 1000 entries with least-recently-used eviction, and SHALL NOT persist them. It SHALL NOT store an `unavailable` text: a later request SHALL send only the texts still missing. Concurrent requests for the same (job, language) SHALL share one provider call.

#### Scenario: Repeated request
- **WHEN** the same job is localized into `ar` twice and the first answer was `translated`
- **THEN** both answers have the same texts and the provider received one request

#### Scenario: Failure is retried
- **WHEN** the first provider call fails and a second request is made
- **THEN** the second request calls the provider again

#### Scenario: Concurrent requests
- **WHEN** ten requests for the same job and language arrive while the provider call is in flight
- **THEN** the provider receives one request and all ten answers carry its translations

### Requirement: Localization logging
A failed provider call SHALL be logged with the job id, the language and a fixed reason only. Logs and response bodies SHALL NOT contain the provider URL, API key, configured header values, prompt, texts or provider messages.

#### Scenario: Provider error
- **WHEN** the provider answers 503 with a body naming its URL and key
- **THEN** the response is 200 with `unavailable` texts and neither the body nor the log contains the URL, the key or the provider message

### Requirement: Localization tests
Tests SHALL use a fake provider and cover every status and error path, the source-language rules, the orchestrator fixed text, cache hits and misses, no caching of `unavailable` results, deduplication, job immutability (deep equality of the stored job and byte equality of `GET /api/v1/jobs/{id}`) and the absence of events, and one test SHALL localize a job loaded from the SQLite store.

#### Scenario: Suite
- **WHEN** `go test ./...` runs
- **THEN** these tests pass without network access
