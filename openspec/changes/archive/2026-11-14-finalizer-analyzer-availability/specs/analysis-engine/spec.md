## ADDED Requirements

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
