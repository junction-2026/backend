## ADDED Requirements

### Requirement: Job risk persistence
Both stores SHALL persist and return `Job.risk` verbatim as part of the stored Job, without a schema migration. A job stored before this change SHALL be read with no risk.

#### Scenario: Restart
- **WHEN** a completed job with a RiskScore is read after the SQLite store was reopened
- **THEN** its `risk` is byte-equal to the one emitted with `analysis.ready_for_finalization`

#### Scenario: Old row
- **WHEN** a stored job JSON has no `risk` member
- **THEN** it loads without error and has no risk
