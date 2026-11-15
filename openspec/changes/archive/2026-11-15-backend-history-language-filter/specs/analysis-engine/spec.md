## ADDED Requirements

### Requirement: Persisted job language
When a job's `translation` run settles, and when startup recovery marks it `failed` (`interrupted`), the job SHALL be written to the store so that its persisted language is updated consistently: the code on success, unknown otherwise. The persisted language SHALL never change after the run succeeded.

#### Scenario: Restart during translation
- **WHEN** the server stops while a job's `translation` run is running and recovery interrupts the job
- **THEN** the job is listed for `language=unknown`

#### Scenario: Succeeded translation
- **WHEN** a job's `translation` run succeeded with `source_language: "de"` and the job later completes
- **THEN** it is listed for `language=de` before and after completion
