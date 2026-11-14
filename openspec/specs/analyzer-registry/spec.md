# analyzer-registry Specification

## Purpose
Backend-local format, loading and validation rules of the analyzer registry that is read from ANALYZERS_CONFIG at startup, including definition fields, environment placeholder expansion and the startup-abort behaviour for invalid registries.

## Requirements

### Requirement: Registry loading
The backend SHALL load the registry at startup from the path in `ANALYZERS_CONFIG`, which is either one YAML file or a directory whose `*.yaml` and `*.yml` files (not recursive, in name order) are all loaded. Each file SHALL have the top-level member `analyzers`, a list of one or more analyzer definitions, and unknown members SHALL be rejected. A file whose `analyzers` list is empty or missing (including an empty file), and a directory without any `*.yaml`/`*.yml` file, SHALL abort startup.

#### Scenario: Empty registry
- **WHEN** a file is empty, is `{}` or has `analyzers: []`
- **THEN** loading fails with an error naming the file

#### Scenario: Directory of files
- **WHEN** `ANALYZERS_CONFIG` is a directory with `a.yaml` (analyzers `a`, `c`), `b.yml` (analyzer `b`) and `notes.txt`
- **THEN** the registry contains `a`, `b` and `c` ordered by id and `notes.txt` is ignored

#### Scenario: Unknown member
- **WHEN** a definition contains the member `colour`
- **THEN** loading fails

### Requirement: Invalid registry aborts startup
An invalid registry SHALL abort startup with an error naming the file and the analyzer id, and an analyzer id SHALL be unique across all files.

#### Scenario: Error names file and analyzer
- **WHEN** analyzer `claims` in `claims.yaml` depends on an unknown analyzer
- **THEN** loading fails with an error containing `claims.yaml` and `"claims"`

#### Scenario: Duplicate id across files
- **WHEN** two files define analyzer `a`
- **THEN** loading fails naming the second file, `"a"` and that the id is a duplicate

### Requirement: Analyzer definition fields
An analyzer definition SHALL have: `id` (matching `^[a-z][a-z0-9_]{0,62}$`), `name` (default: the id), `category`, `enabled` (default `true`), `trigger` (`on_dependencies_succeeded`, the default, or `after_all_analyzers_settled`), `depends_on` (list of ids) and `execution`.

#### Scenario: Defaults
- **WHEN** a definition only sets `id` and an `http_sync` execution with an endpoint
- **THEN** the loaded analyzer is enabled, has trigger `on_dependencies_succeeded` and name equal to its id

#### Scenario: Invalid id
- **WHEN** a definition has the id `Bad-ID`
- **THEN** loading fails because the id does not match the id pattern

### Requirement: Applicability, bindings and output fields
A definition SHALL accept the optional members `applies_to` with `content_types` (list), `requires_source_url` (boolean) and `min_content_length` (integer), `input.bindings` (map of input name to binding source) and `output.schema` (JSON Schema, draft 2020-12).

#### Scenario: Optional fields
- **WHEN** a definition omits `applies_to`, `input` and `output`
- **THEN** the analyzer applies to every input, gets the default inputs and has no output schema

### Requirement: Execution settings
`execution` SHALL have `adapter` (`http_sync`, `http_async` or `custom`), `endpoint`, `method` (default `POST`), `headers` (map), `timeout` (duration, default `30s`), `poll_interval` (duration, default `1s`, used by `http_async`) and `handler` (`custom` only).

#### Scenario: Execution defaults
- **WHEN** an `http_async` execution only sets an endpoint
- **THEN** method is `POST`, timeout 30s and poll interval 1s

### Requirement: Environment placeholders
`${VAR}` placeholders in `execution.endpoint` and in `execution.headers` values SHALL be expanded from the process environment once at load time. A variable that is not set and is referenced by an enabled analyzer SHALL abort startup with an error naming the variable and the analyzer; disabled analyzers are not expanded. Expanded values SHALL NOT be logged and no error message SHALL contain one.

#### Scenario: Expansion
- **WHEN** an enabled analyzer has `endpoint: ${BASE}/sentiment` and `BASE=http://svc`
- **THEN** its endpoint is `http://svc/sentiment`

#### Scenario: Unset variable
- **WHEN** an enabled analyzer references `${NOPE}` and `NOPE` is not set
- **THEN** startup aborts with an error naming `NOPE` and the analyzer

#### Scenario: Disabled analyzer
- **WHEN** a disabled analyzer references an unset variable
- **THEN** loading succeeds

### Requirement: Dependency graph validation
Loading SHALL reject `depends_on` ids that are not defined, dependency cycles (including a self dependency), more than one enabled analyzer with trigger `after_all_analyzers_settled`, and `depends_on` on, or pointing to, an `after_all_analyzers_settled` analyzer.

#### Scenario: Cycle
- **WHEN** `a` depends on `b` and `b` depends on `a`
- **THEN** loading fails with a dependency cycle error

#### Scenario: Two finalizers
- **WHEN** two enabled analyzers have trigger `after_all_analyzers_settled`
- **THEN** loading fails

#### Scenario: Dependency on the finalizer
- **WHEN** a regular analyzer lists an `after_all_analyzers_settled` analyzer in `depends_on`
- **THEN** loading fails

### Requirement: Binding validation
Known binding sources are `$input`, `$input.source`, `$job.id`, `$job.analyzers`, `$job.coverage` and `$analyzers.<id>`. Loading SHALL reject unknown sources, `$job.analyzers` and `$job.coverage` on analyzers that are not `after_all_analyzers_settled`, and `$analyzers.<id>` bindings whose id is not in the analyzer's `depends_on`.

#### Scenario: Job-wide binding on a regular analyzer
- **WHEN** a regular analyzer binds `$job.analyzers`
- **THEN** loading fails

#### Scenario: Binding without dependency
- **WHEN** analyzer `b` binds `$analyzers.a` without listing `a` in `depends_on`
- **THEN** loading fails

### Requirement: Execution validation
Loading SHALL reject a missing `endpoint` for the http adapters, a `custom` analyzer whose `handler` is not a registered handler name, unknown adapters and triggers, non-positive durations and invalid output schemas.

#### Scenario: Unknown custom handler
- **WHEN** a `custom` analyzer names the handler `nope`
- **THEN** loading fails with an error naming the handler

#### Scenario: Missing endpoint
- **WHEN** an `http_sync` analyzer has no endpoint
- **THEN** loading fails

#### Scenario: Invalid output schema
- **WHEN** an analyzer declares `output.schema` `{type: 12}`
- **THEN** loading fails

### Requirement: Output schema validation
When an analyzer declares `output.schema`, its result SHALL be validated against that JSON Schema (draft 2020-12) before it is stored. A violation SHALL fail the analyzer with `error.code` `invalid_output` and a message that names the first violated instance path.

#### Scenario: Wrong type
- **WHEN** the schema requires `score` to be a number and the analyzer returns `{"score": "high"}`
- **THEN** the analyzer is `failed` with `invalid_output` and the message contains `/score`

### Requirement: Selection closure
The registry SHALL compute, for a list of analyzer ids, the set containing those ids and all their transitive dependencies.

#### Scenario: Transitive dependencies
- **WHEN** `c` depends on `b`, `b` on `a` and `c` is selected
- **THEN** the closure is `a`, `b` and `c`
