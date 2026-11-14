## ADDED Requirements

### Requirement: Job public field
The Job SHALL carry a `public` field set at creation (true unless the request makes it non-public) that never changes afterwards, and SHALL serialize it in every Job representation: REST bodies, the `data.job` of `job.snapshot` and of the terminal events, and persisted history.

#### Scenario: Unlisted job events
- **WHEN** a client follows or cancels a job created with `public: false`
- **THEN** `data.job` of the snapshot and of the terminal event has `public: false`

#### Scenario: Restart
- **WHEN** the server restarts with persistent history
- **THEN** a job created with `public: false` is still `public: false` and not listed
