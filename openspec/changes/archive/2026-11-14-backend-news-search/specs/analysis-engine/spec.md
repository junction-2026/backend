## ADDED Requirements

### Requirement: Adapter-reported skip
An adapter MAY end a run it was executing as skipped by returning a skip outcome with a reason. The engine SHALL settle the running run as `skipped` with that reason and `result: null`, emitting `analyzer.skipped` after `analyzer.started`; analyzers depending on it SHALL be skipped with `dependency_skipped`.

#### Scenario: Skip after start
- **WHEN** an adapter returns a skip outcome with reason `no_searchable_topics`
- **THEN** the events are `analyzer.started` then `analyzer.skipped` with that reason, the run has `result: null` and a dependent run is `skipped` with `dependency_skipped`
