## ADDED Requirements

### Requirement: Outlet table loading
When an enabled analyzer uses adapter `source_info`, the backend SHALL read `execution.outlets_file` once at startup into memory. `outlets_file` SHALL default to `/etc/orchestrator/data/outlets.csv` and the file SHALL exist and parse; otherwise startup SHALL fail with an error naming the analyzer id and the file. A disabled analyzer SHALL NOT read its file.

#### Scenario: File missing
- **WHEN** an enabled `source_info` analyzer's `outlets_file` does not exist
- **THEN** startup fails with an error that names analyzer `source_info` and the file path

#### Scenario: Disabled analyzer
- **WHEN** a `source_info` analyzer with `enabled: false` names a missing file
- **THEN** startup succeeds

### Requirement: Outlet table header
The first CSV row of the outlet table SHALL equal `domain,domains,hosts,name,country_code,country,bias,factuality,site_lang`; otherwise startup SHALL fail with an error naming the file and row 1.

#### Scenario: Wrong header
- **WHEN** the first row is `domain,name`
- **THEN** startup fails naming the file and row 1

### Requirement: Outlet table row validation
Startup SHALL fail with the 1-based row number and the offending value when a row has an empty `domain`, or lists a value in `domains` or `hosts` that was already seen under another outlet (case-insensitive). The values of `domains` and `hosts` are `|`-separated, trimmed and lower-cased, and empty items are ignored. A value repeated within one outlet is not an error.

#### Scenario: Host under two outlets
- **WHEN** row 3 lists host `x.a.com`, which row 2 (another outlet) already lists
- **THEN** startup fails with an error naming row 3 and `x.a.com`

#### Scenario: Empty domain
- **WHEN** row 3 has an empty `domain`
- **THEN** startup fails with an error naming row 3

### Requirement: Outlet table lookup maps
The loaded table SHALL provide a host map built from every outlet's `hosts` and `domains` and a domain map built from every outlet's `domains`, both keyed by the lower-case value without a leading `www.`.

#### Scenario: Maps
- **WHEN** an outlet has domains `bbc.co.uk|bbc.com` and hosts `news.bbc.co.uk`
- **THEN** `news.bbc.co.uk`, `bbc.co.uk` and `bbc.com` are in the host map and `bbc.co.uk` and `bbc.com` are in the domain map

### Requirement: Outlet table sharing
Analyzers that refer to the same `outlets_file` SHALL share one loaded table. The table SHALL NOT change after loading and SHALL be safe for concurrent reads.

#### Scenario: Two analyzers, one file
- **WHEN** two enabled `source_info` analyzers name the same file
- **THEN** both use the same loaded table

### Requirement: Source info registry keys
Adapter `source_info` SHALL accept `execution.outlets_file` only, plus `execution.timeout`; `outlets_file` SHALL be rejected on other adapters. `depends_on` SHALL include the id of a `news_search` analyzer and `input.bindings.search` SHALL be `$analyzers.<that id>`; `input.bindings.source` MAY be `$input.source`. The trigger SHALL be `on_dependencies_succeeded`. Any other key or binding SHALL be a startup error.

#### Scenario: Other execution key
- **WHEN** a `source_info` analyzer sets `execution.endpoint`
- **THEN** startup fails naming the analyzer and the allowed keys

#### Scenario: Search is not news_search
- **WHEN** `input.bindings.search` is `$analyzers.topic_extraction` and that analyzer does not run adapter `news_search`
- **THEN** startup fails

### Requirement: Source info image and example
The `Dockerfile` SHALL contain `COPY data/outlets.csv /etc/orchestrator/data/outlets.csv`. `docs/examples/source_info.yaml` SHALL hold the registry entry of `source_info` including `llm.include: true` and its description, and the README SHALL document `source_info` and `outlets_file`.

#### Scenario: Example loads
- **WHEN** the example is loaded together with `news_search`
- **THEN** the registry accepts it
