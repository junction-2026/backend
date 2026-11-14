## MODIFIED Requirements

### Requirement: Job localization errors
The handler SHALL answer, in the api-conventions error envelope, a missing or blank `language` with 400 `invalid_request`, a `language` whose lower-case primary subtag is not supported with 400 `unsupported_language` and `error.details.supported` listing `en`, `fr`, `ar`, `de`, `fi` and `ru`, an unknown job with 404 `not_found` and a job that is not done with 409 `job_not_finished`.

#### Scenario: Unsupported language
- **WHEN** a client calls `GET /api/v1/jobs/j1/localization?language=xx`
- **THEN** the status is 400, `error.code` is `unsupported_language` and `error.details.supported` is `["en","fr","ar","de","fi","ru"]`

#### Scenario: Missing language
- **WHEN** a client calls `GET /api/v1/jobs/j1/localization` without `language`
- **THEN** the status is 400 with `error.code` `invalid_request`

#### Scenario: Region subtag
- **WHEN** a client calls it with `language=DE-at` for a done job
- **THEN** the status is 200 and `language` is `de`

#### Scenario: Russian accepted
- **WHEN** a client calls `GET /api/v1/jobs/j1/localization?language=ru-RU` for a done job
- **THEN** the status is 200 and `language` is `ru`
