## ADDED Requirements

### Requirement: Create body visibility flag
The create handler SHALL decode `public` as an optional strict JSON boolean (default true), SHALL reject `null` and any non-boolean with 422 `validation_failed` and `details.fields.public`, and SHALL validate it together with the other create-body checks for both `input` and `scrape` bodies, creating no job when any check fails.

#### Scenario: Default and explicit values
- **WHEN** a client posts a body without `public`, with `"public": true`, or with `"public": false`
- **THEN** the status is 201 and the Job has `public` true, true and false respectively

#### Scenario: Invalid values
- **WHEN** a client posts `"public": "no"`, `1` or `null` with an `input` or a `scrape` body
- **THEN** the status is 422 `validation_failed`, `error.details.fields` has the key `public` and no job is created
