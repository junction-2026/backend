## ADDED Requirements

### Requirement: Adapter-reported skip
An adapter SHALL be able to report that its run has nothing to do. The engine SHALL then settle that run as `skipped` with the adapter's reason, `error: null`, `result: null` and `started_at` kept, emit exactly one `analyzer.skipped` event after `analyzer.started`, and resolve dependent analyzers as for any skipped dependency.

#### Scenario: Runtime skip
- **WHEN** an analyzer's adapter reports the reason `no_checkable_claims`
- **THEN** the event stream has `analyzer.started` then one `analyzer.skipped` with that reason for it, the run has `started_at` set and `result: null`, and a dependent analyzer is skipped with `dependency_skipped`
