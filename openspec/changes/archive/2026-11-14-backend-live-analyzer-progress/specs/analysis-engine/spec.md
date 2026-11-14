## ADDED Requirements

### Requirement: Progress state on the job record
The job record SHALL store the plan, the current Progress, the Activity of each running planned analyzer (and of the finalizer while it runs) and the preview of each succeeded planned analyzer, all guarded by the job mutex. `job.snapshot` SHALL be built from this stored state, without recomputing from results, and SHALL carry copies that stay valid after the mutex is released. None of it SHALL be a member of the Job object.

#### Scenario: Snapshot equals events
- **WHEN** a client connects while `news_search` runs after `translation` succeeded
- **THEN** the snapshot's `progress` equals the Progress of the last event and `activities` holds the `news_search` Activity of its `analyzer.started`

### Requirement: Activity texts
Activity texts SHALL come from one built-in table keyed by activity key: `translation` "Translating the text", `propaganda_detection` "Checking for propaganda", `techniques` "Detecting persuasion techniques", `llm_techniques` "Looking for misleading framing", `topic_extraction` "Finding topics and claims".

#### Scenario: Known key
- **WHEN** `techniques` starts
- **THEN** its Activity text is "Detecting persuasion techniques"

### Requirement: Activity texts for lookups and special keys
The same table SHALL hold `news_search` "Searching news coverage", `fact_check` "Looking up fact-checks", `source_info` "Checking the news outlets", `finalizer` "Writing the verdict" and `scrape` "Fetching the content". A key not in the table SHALL use the analyzer name.

#### Scenario: Unknown key
- **WHEN** an analyzer named "Bot detection" with key `bot_detection` starts
- **THEN** its Activity text is "Bot detection"

### Requirement: Language names
The `news_search` activity detail SHALL name languages from a fixed English table covering at least de, en, es, fi, fr, it, nl, pl, pt, ru, sv and uk, and SHALL show a code not in the table as the code.

#### Scenario: Unlisted code
- **WHEN** `news_search` will search `de` and `ja`
- **THEN** the detail is "in German and ja"

### Requirement: Activity facts match the run
Activity facts SHALL be computed with the functions the adapters use for the same decision: `news_search` language resolution, `fact_check` qualifying-claim selection, `techniques` model selection by word count and `llm_techniques` headline check. A fact that cannot be determined SHALL be left out of `params`.

#### Scenario: Same decision
- **WHEN** a translation has 81 words with the default bounds
- **THEN** the `techniques` Activity names `nci-technique` and the adapter calls only `nci-technique`

### Requirement: Preview builders
Each preview SHALL be built by a pure function from the result JSON, one per analyzer id, with table-driven tests that include missing members, wrong-typed members and older results without `plain_language`.

#### Scenario: Wrong-typed members
- **WHEN** a `fact_check` result has `fact_checks: "x"`
- **THEN** its preview has `fact_checks: null` and `top: []`

### Requirement: Builders never disturb the run
Building a preview or an Activity SHALL NOT perform a network, LLM or translation call and SHALL NOT panic the job. A builder error or panic SHALL yield preview null (or an Activity without facts) and leave the run's outcome and other event members unchanged.

#### Scenario: Panicking builder
- **WHEN** a preview builder panics on a result
- **THEN** the `analyzer.succeeded` event has `preview: null` and the job completes

### Requirement: Progress documented
The README SHALL document `analysis.planned`, Activity, Progress, the preview shapes, the snapshot members and the registry `ui` block.

#### Scenario: Documentation present
- **WHEN** a reader opens the README
- **THEN** it has a section on live progress events naming every item above

### Requirement: Engine event tests
Engine tests SHALL cover event order for text and scrape jobs and progress counting with `not_applicable`, `not_selected`, hidden, failed, dependency-skipped and cancelled analyzers, `finalize: false` and a registry without a finalizer.

#### Scenario: Coverage of progress cases
- **WHEN** the engine tests run offline with stub adapters
- **THEN** each case above asserts `progress` and the plan on the emitted events
