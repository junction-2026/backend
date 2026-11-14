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
`execution` SHALL have `adapter` (`http_sync`, `http_async`, `propaganda_detection`, `mistral_translation`, `custom` or `llm`), `endpoint`, `method` (default `POST`), `headers`, `timeout` (default `30s`; see LLM settings), `poll_interval` (default `1s`, used by `http_async`) and `handler` (`custom` only). The adapter `propaganda_detection` SHALL use `endpoint`, `headers` and `timeout`; its `method` SHALL be absent or `POST`, and `handler` and `poll_interval` SHALL be rejected for it.

#### Scenario: Execution defaults
- **WHEN** an `http_async` execution only sets an endpoint
- **THEN** method is `POST`, timeout 30s and poll interval 1s

#### Scenario: Propaganda detection settings
- **WHEN** a `propaganda_detection` execution sets an endpoint and headers and omits `method` and `timeout`
- **THEN** it loads with method `POST` and timeout 30s

#### Scenario: Propaganda detection rejected settings
- **WHEN** a `propaganda_detection` execution sets `method: GET`, a `handler` or a `poll_interval`
- **THEN** loading fails with an error naming the file and analyzer id

### Requirement: Environment placeholders
`${VAR}` placeholders in `execution.endpoint`, in `execution.headers` values and in every string of the `llm` settings (including inside `extra_body`) SHALL be expanded from the process environment once at load time. A variable that is not set and is referenced by an enabled analyzer SHALL abort startup with an error naming the variable and the analyzer; disabled analyzers are not expanded. Expanded values SHALL NOT be logged and no error message SHALL contain one.

#### Scenario: Expansion
- **WHEN** an enabled analyzer has `endpoint: ${BASE}/sentiment` and `BASE=http://svc`
- **THEN** its endpoint is `http://svc/sentiment`

#### Scenario: Unset variable
- **WHEN** an enabled analyzer references `${NOPE}` and `NOPE` is not set
- **THEN** startup aborts with an error naming `NOPE` and the analyzer

#### Scenario: Disabled analyzer
- **WHEN** a disabled analyzer references an unset variable
- **THEN** loading succeeds

#### Scenario: LLM values
- **WHEN** an enabled `llm` finalizer has `headers: {X-Title: "${V}"}` and `extra_body: {provider: {order: ["${V}"]}}` with `V=x`
- **THEN** the header value is `x` and `extra_body.provider.order` is `["x"]`

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
Loading SHALL reject a missing or empty `endpoint` for the http adapters and for `propaganda_detection`, `input.bindings` on a `propaganda_detection` analyzer other than the `text` binding, a `custom` analyzer whose `handler` is not a registered handler name, unknown adapters and triggers, non-positive durations and invalid output schemas. The errors SHALL name the file and the analyzer id, and `${VAR}` expansion of endpoint and header values applies to `propaganda_detection` unchanged.

#### Scenario: Unknown custom handler
- **WHEN** a `custom` analyzer names the handler `nope`
- **THEN** loading fails with an error naming the handler

#### Scenario: Missing endpoint
- **WHEN** an `http_sync` or `propaganda_detection` analyzer has no endpoint or an empty one
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: Bindings on propaganda detection
- **WHEN** a `propaganda_detection` analyzer binds a name other than `text`, or `text` to `$input.source`, a `$job.*` source or an `$analyzers.<id>` whose id is not in `depends_on`
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: Propaganda detection placeholders
- **WHEN** an enabled `propaganda_detection` analyzer has `endpoint: ${PROPAGANDA_DETECTION_URL}`
- **THEN** it loads when the variable is set, loading fails naming the variable when it is unset, and a disabled analyzer with the unset variable loads

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

### Requirement: Propaganda detection example
`docs/examples/propaganda_detection.yaml` SHALL define the analyzer `translation` (name `Translation to English`, category `translation`, adapter `mistral_translation`, `api_key: ${MISTRAL_API_KEY}`, model `mistral-small-latest`, timeout `60s`) and the analyzer `propaganda_detection` (adapter `propaganda_detection`, endpoint `${PROPAGANDA_DETECTION_URL}`, timeout `30s`, `depends_on: [translation]`, `input.bindings: {text: "$analyzers.translation"}`) with its `llm` opt-in block.

#### Scenario: Example loads
- **WHEN** the example is loaded with `MISTRAL_API_KEY` and `PROPAGANDA_DETECTION_URL` set
- **THEN** analyzers `translation` and `propaganda_detection` are loaded and enabled, the latter depending on the former with the `text` binding and keeping its `llm` block

#### Scenario: Documentation and sample registry
- **WHEN** the README is read
- **THEN** it documents `MISTRAL_API_KEY`, `PROPAGANDA_DETECTION_URL`, the model, timeout, `max_input_chars`, truncation, the cost and latency of the extra call and the dependency, and how to add the example to `ANALYZERS_CONFIG`, while `config/analyzers` contains neither analyzer (an enabled analyzer with an unset variable aborts startup)

### Requirement: LLM execution settings
The `llm` adapter SHALL take `task` (`final_synthesis` default or `topic_extraction`), `base_url`, `api_key` and `model` (required, no default), `prompt_file`, `temperature` (default 0), `max_tokens`, `timeout` (30s), `response_format` (`json_schema` default, `json_object`, `none`), `headers` and `extra_body` (default `{}`), all with `${VAR}` expansion, and for `topic_extraction` `max_input_chars` (positive integer, default 20000). `prompt_file` and `max_tokens` defaults depend on the task.

#### Scenario: LLM defaults
- **WHEN** an `llm` finalizer sets only `base_url`, `api_key`, `model` and `prompt_file`
- **THEN** it loads with temperature 0, max_tokens 600, timeout 30s, response_format `json_schema`, no headers and an empty `extra_body`, and a missing `prompt_file` defaults to `/etc/orchestrator/finalizer/prompt.md`

#### Scenario: Topic extraction defaults
- **WHEN** a regular analyzer sets `adapter: llm`, `task: topic_extraction`, `base_url`, `api_key` and `model`
- **THEN** it loads with `prompt_file` `/etc/orchestrator/extractor/prompt.md`, max_tokens 2000, timeout 30s, max_input_chars 20000 and response_format `json_schema`

#### Scenario: Invalid input limit
- **WHEN** a `topic_extraction` analyzer has `max_input_chars: 0`
- **THEN** loading fails with an error naming the analyzer id and `execution.max_input_chars`

### Requirement: LLM execution validation
Loading SHALL accept the `llm` adapter on an `after_all_analyzers_settled` analyzer whose `task` is absent or `final_synthesis`, and on an `on_dependencies_succeeded` analyzer only with `task: topic_extraction`, and reject every other trigger and task combination, `input.bindings` on `topic_extraction`, an `extra_body` with `model` or `messages`, and an invalid `response_format`. The errors SHALL name the file, the analyzer id and the field.

#### Scenario: LLM adapter on a regular analyzer
- **WHEN** an analyzer with trigger `on_dependencies_succeeded` has `adapter: llm` and no `task`
- **THEN** loading fails with an error naming the analyzer id and `execution.adapter`

#### Scenario: Topic extraction on a finalizer
- **WHEN** an `after_all_analyzers_settled` analyzer has `adapter: llm` and `task: topic_extraction`
- **THEN** loading fails with an error naming the analyzer id and `execution.task`

#### Scenario: Final synthesis on a regular analyzer
- **WHEN** an `on_dependencies_succeeded` analyzer has `adapter: llm` and `task: final_synthesis`
- **THEN** loading fails with an error naming the analyzer id and `execution.adapter`

#### Scenario: Unknown response format
- **WHEN** an enabled `llm` analyzer has `response_format: xml`
- **THEN** loading fails with an error naming the analyzer id and `execution.response_format`

#### Scenario: Extra body replaces the model
- **WHEN** an `llm` analyzer has `extra_body: {model: other}`
- **THEN** loading fails with an error naming `execution.extra_body.model`

### Requirement: LLM finalizer startup checks
For an enabled `llm` finalizer or `topic_extraction` analyzer, loading SHALL abort startup when `base_url`, `api_key` or `model` is empty after expansion or `prompt_file` cannot be read, with a message naming the analyzer and the field and never containing the API key. The prompt file SHALL be read once at load. A disabled `llm` analyzer SHALL NOT be checked. The API key SHALL NOT be logged or returned by any API.

#### Scenario: Empty key
- **WHEN** `api_key: ${FINALIZER_API_KEY}` and `FINALIZER_API_KEY` is set to an empty string
- **THEN** startup fails with a message naming `execution.api_key`

#### Scenario: Unreadable prompt
- **WHEN** `prompt_file` names a file that does not exist
- **THEN** startup fails with a message naming `execution.prompt_file`

#### Scenario: Disabled finalizer
- **WHEN** the `llm` finalizer is `enabled: false` and its variables are unset
- **THEN** the server starts

#### Scenario: Topic extraction missing model
- **WHEN** an enabled `topic_extraction` analyzer has `model: ${EXTRACTOR_MODEL}` and the variable is empty
- **THEN** startup fails with a message naming the analyzer and `execution.model`

### Requirement: Analyzer LLM input block
An analyzer MAY have `llm: {include: bool (default false), description: string}`. `include: true` with a description that is empty after trimming SHALL abort startup, and the block on an `after_all_analyzers_settled` analyzer SHALL be rejected; both errors name the analyzer and the field.

#### Scenario: Opted-in analyzer
- **WHEN** `propaganda_detection` has `llm: {include: true, description: "Binary classifier ..."}`
- **THEN** it loads and its runs are sent to the `llm` finalizer with that description

#### Scenario: Missing description
- **WHEN** an analyzer has `llm: {include: true}`
- **THEN** loading fails naming `llm.description`

#### Scenario: Block on the finalizer
- **WHEN** the finalizer itself has an `llm` block
- **THEN** loading fails

### Requirement: LLM finalizer example
`docs/examples/final_synthesis.yaml` SHALL be the `llm` finalizer entry with `base_url: ${FINALIZER_BASE_URL}`, `api_key: ${FINALIZER_API_KEY}` and `model: ${FINALIZER_MODEL}`, and `docs/examples/propaganda_detection.yaml` SHALL have `llm: {include: true, description: ...}` with the description from the parent issue. The README SHALL document these variables and the `response_format` modes.

#### Scenario: Examples run
- **WHEN** both examples and `text_stats` are loaded with the variables pointing at stubs
- **THEN** a job ends `completed` with a `detected` result and the LLM request contains `propaganda_detection` with its description and no `text_stats`

### Requirement: Propaganda detection text binding
A `propaganda_detection` analyzer MAY have `input.bindings` with exactly one member `text` whose source is `$input` (the default behaviour) or `$analyzers.<id>` with `<id>` in `depends_on`. Any other binding name or source SHALL be rejected at load with an error naming the file and the analyzer id.

#### Scenario: Binding to a translation
- **WHEN** `propaganda_detection` has `depends_on: [translation]` and `input.bindings: {text: "$analyzers.translation"}`
- **THEN** it loads

#### Scenario: Other binding
- **WHEN** it binds `other: $input`, `text: $input.source`, `text: $job.id` or `text: $analyzers.x` with `x` not in `depends_on`
- **THEN** loading fails with an error naming the file and the analyzer id

#### Scenario: No bindings
- **WHEN** a `propaganda_detection` analyzer has no `input.bindings`
- **THEN** it loads and behaves as before

### Requirement: Mistral translation settings
The `mistral_translation` adapter SHALL take `endpoint` (default `https://api.mistral.ai/v1/chat/completions`), `api_key` (required), `model` (default `mistral-small-latest`), `timeout` (default `60s`), `max_input_chars` (positive integer, default `20000`) and `headers`, and the English skip settings. `method`, `handler`, `poll_interval` and `input.bindings` SHALL be rejected for it.

#### Scenario: Mistral translation defaults
- **WHEN** a `mistral_translation` execution only sets `api_key`
- **THEN** it loads with endpoint `https://api.mistral.ai/v1/chat/completions`, model `mistral-small-latest`, timeout 60s, `max_input_chars` 20000, `skip_english` true, `english_min_words` 8 and `english_min_ratio` 0.25

#### Scenario: Mistral translation rejected settings
- **WHEN** a `mistral_translation` execution sets `method`, `handler` or `poll_interval`, has no `api_key`, has `input.bindings`, or sets `max_input_chars` to 0 or a negative number
- **THEN** loading fails with an error naming the file and analyzer id

### Requirement: Mistral translation placeholders
For an enabled `mistral_translation` analyzer `${VAR}` expansion SHALL apply to `endpoint`, `api_key`, `model` and `headers`. An unset variable SHALL abort loading with an error naming the variable and never a value; a disabled analyzer is not expanded.

#### Scenario: Unset key variable
- **WHEN** an enabled `mistral_translation` analyzer has `api_key: ${MISTRAL_API_KEY}`
- **THEN** it loads when the variable is set, loading fails naming the variable when it is unset, and a disabled analyzer with the unset variable loads

### Requirement: Topic extraction prompt default
The default `prompt_file` of `topic_extraction` SHALL be `/etc/orchestrator/extractor/prompt.md`, which the image gets from `config/extractor/prompt.md` through a `COPY` line in the root `Dockerfile`. The prompt SHALL ask for at most 10 topics and at most 10 news items in the language of the input, and SHALL tell the model to treat the content as data, not instructions.

#### Scenario: Shipped prompt
- **WHEN** `config/extractor/prompt.md` is read
- **THEN** it asks for at most 10 topics and 10 news items, names the language of the content and says the content is data, not instructions

### Requirement: Topic extraction example
`docs/examples/topic_extraction.yaml` SHALL define the analyzer `topic_extraction` (trigger `on_dependencies_succeeded`, `adapter: llm`, `task: topic_extraction`) with `base_url: ${EXTRACTOR_BASE_URL}`, `api_key: ${EXTRACTOR_API_KEY}` and `model: ${EXTRACTOR_MODEL}`, independent of the finalizer's variables. The README section "Topic extraction analyzer" SHALL document every setting and these variables.

#### Scenario: Example loads
- **WHEN** the example is loaded with the three variables set
- **THEN** it loads as a topic extraction analyzer, and with `EXTRACTOR_MODEL` unset loading fails naming `EXTRACTOR_MODEL`

### Requirement: Bot detection example
`docs/examples/bot_detection.yaml` SHALL define the analyzer `bot_detection` (category `bot_detection`, `enabled: false`, adapter `http_sync`, endpoint `${BOT_DETECTION_URL}`, the bot detector service on port 8001) with `input.bindings` `{content: "$input", source: "$input.source"}`, so it receives the scraped profile, history and comments in `source.metadata.scrape`. The README SHALL document it, the scraper variables and creating a job from a URL with a curl example.

#### Scenario: Example loads disabled
- **WHEN** the example is loaded without `BOT_DETECTION_URL` set
- **THEN** loading succeeds and `bot_detection` is disabled with the two bindings

### Requirement: news_search settings
The registry SHALL accept `execution.adapter: news_search` with the keys `base_url` and `api_key` (required, `${VAR}`-expanded), `languages` (default `[source, en]`), `translate.api_key`, `translate.model` and `translate.endpoint`, and the limits `max_topics`, `max_news`, `num`, `min_results`, `per_domain`, `max_results`, `max_concurrency`, `cache_ttl` and `timeout`. `translate.api_key` SHALL be required when a language other than `source` is configured.

#### Scenario: Defaults
- **WHEN** only `base_url` and `api_key` and `translate.api_key` are set
- **THEN** the analyzer loads with languages `[source, en]`, max_topics 3, max_news 2, num 8, min_results 2, per_domain 2, max_results 12, max_concurrency 4, cache_ttl 6h and timeout 20s

### Requirement: news_search settings are validated
A non-positive integer or duration, an unknown language token, a missing required key or a `news_search` key on another adapter SHALL fail startup with a message naming the key.

#### Scenario: Invalid values
- **WHEN** `num: 0`, `languages: [german]`, a missing `api_key` or a missing `translate.api_key` with `languages: [source, en]` is configured
- **THEN** startup fails and the message names `execution.num`, `execution.languages`, `execution.api_key` or `execution.translate.api_key`

#### Scenario: Source only needs no translation key
- **WHEN** `languages: [source]` and no `translate` block
- **THEN** the analyzer loads

### Requirement: news_search dependencies and bindings
A `news_search` analyzer SHALL list `topic_extraction` in `depends_on` and bind `input.bindings.topics: $analyzers.topic_extraction`. It MAY bind `translation: $analyzers.<id>`, which SHALL be in `depends_on`. Other bindings, `endpoint`, `method`, `handler` and `poll_interval` SHALL be rejected.

#### Scenario: Missing topics binding
- **WHEN** `news_search` has no `topics` binding or does not depend on `topic_extraction`
- **THEN** startup fails naming `input.bindings.topics` or `depends_on`

#### Scenario: Translation binding outside depends_on
- **WHEN** `translation: $analyzers.translation` is bound but `translation` is not in `depends_on`
- **THEN** startup fails naming the binding

### Requirement: Fact check settings
Adapter `fact_check` SHALL accept the execution keys `base_url`, `api_key` (required when enabled), `min_check_worthiness`, `max_claims`, `page_size`, `max_age_days`, `max_results`, `max_concurrency`, `cache_ttl` and `timeout`, with the defaults of `docs/examples/fact_check.yaml` and `${VAR}` expansion in `base_url` and `api_key`. Loading SHALL fail, naming analyzer id and field, for an invalid value, a missing `api_key` or other `input.bindings`.

#### Scenario: Defaults
- **WHEN** an enabled `fact_check` entry sets only `api_key` and the binding
- **THEN** it loads with the defaults above

#### Scenario: Invalid value
- **WHEN** `min_check_worthiness` is 1.5, or `max_claims` is 0
- **THEN** loading fails with an error naming the analyzer id and the field

#### Scenario: Missing key
- **WHEN** an enabled entry has an empty `api_key`
- **THEN** loading fails naming `execution.api_key`, without any value

### Requirement: Topic extraction claim cap
The `topic_extraction` llm entry SHALL accept `execution.max_claims` (>=1, default 5); the key is rejected on every other adapter except `fact_check`.

#### Scenario: Invalid cap
- **WHEN** a `topic_extraction` entry has `max_claims: 0`
- **THEN** loading fails with an error naming the analyzer id and `execution.max_claims`

### Requirement: Fact check example
`docs/examples/fact_check.yaml` SHALL hold the `fact_check` entry (id `fact_check`, category `fact_check`, `depends_on: [topic_extraction]`, binding `extraction`, `api_key: ${GOOGLE_FACTCHECK_API_KEY}` and an `llm` block with `include: true` and a description), `docs/examples/topic_extraction.yaml` SHALL show `max_claims: 5`, and the README SHALL describe `GOOGLE_FACTCHECK_API_KEY` and that the Fact Check Tools API must be enabled on the key's Google Cloud project.

#### Scenario: Example loads
- **WHEN** the example files are loaded with their variables set
- **THEN** the registry is valid

### Requirement: Shared execution keys
`max_results`, `max_concurrency` and `cache_ttl` SHALL be accepted by both `news_search` and `fact_check`, each with its own defaults, and rejected on every other adapter; the other `news_search` keys SHALL be rejected on `fact_check`.

#### Scenario: Both analyzers in one registry
- **WHEN** a registry holds a `news_search` and a `fact_check` entry that both set `max_results`, `max_concurrency` and `cache_ttl`
- **THEN** it loads and each analyzer has its own values

#### Scenario: News-only key on fact_check
- **WHEN** a `fact_check` entry sets `num`
- **THEN** loading fails with an error naming news_search as the only adapter allowing it

### Requirement: Technique classifier settings
The registry SHALL accept `execution.adapter: technique_classifier` with `social_max_words` (positive integer, default 80), `technique_min_words` (positive integer, default 40), `timeout` (default `30s`), the blocks `technique` and `social` and the optional `spans` block. These keys SHALL NOT be accepted on other adapters.

#### Scenario: Defaults and expansion
- **WHEN** an analyzer with `technique` and `social` blocks only is loaded with its variables set
- **THEN** `social_max_words` is 80, `technique_min_words` is 40, `timeout` is 30 seconds and endpoints and header values are expanded

#### Scenario: Keys on another adapter
- **WHEN** an `http_sync` analyzer sets `social_max_words`
- **THEN** loading fails

### Requirement: Technique classifier top-level keys and bindings
Loading SHALL fail, naming the file and the analyzer id, when `execution.endpoint`, `method`, `handler`, `poll_interval` or `headers` is set, or when `input.bindings` is anything other than `text` bound to `$analyzers.<id>` with `<id>` in `depends_on`.

#### Scenario: Wrong binding
- **WHEN** `input.bindings` is `{text: $input}`
- **THEN** loading fails

### Requirement: Technique classifier placeholders
An enabled technique analyzer with an unset `${VAR}` SHALL fail to load naming the variable and never a value; a disabled one SHALL load.

#### Scenario: Disabled analyzer
- **WHEN** a disabled technique analyzer references unset variables
- **THEN** the registry loads

### Requirement: Technique classifier example
`docs/examples/technique_classifier.yaml` SHALL define the analyzer `techniques` (category `propaganda`, `depends_on: [translation]`, binding `text: "$analyzers.translation"`, `social_max_words: 80`, `technique_min_words: 40`, endpoints `${NCI_TECHNIQUE_URL}` and `${NCI_SOCIAL_URL}`, header `Authorization: Bearer ${ML_API_TOKEN}`, cutoffs 0.5 and 0.3, `llm.include: true`) and a `spans` block. The README SHALL document it in its own section with its variables, keys and defaults.

#### Scenario: Example loads
- **WHEN** the example is loaded next to an analyzer `translation` with its variables set
- **THEN** `techniques` is enabled, depends on `translation`, keeps its `llm` block and has a loaded `spans` block

#### Scenario: Span variables
- **WHEN** the example's `spans` block is read
- **THEN** it uses `${TECHNIQUE_SPAN_URL}`, `${SPAN_LLM_BASE_URL}`, `${SPAN_LLM_API_KEY}` and `${SPAN_LLM_MODEL}`

#### Scenario: Span variable missing
- **WHEN** the example is loaded without `SPAN_LLM_API_KEY`
- **THEN** loading fails naming `SPAN_LLM_API_KEY`

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

### Requirement: Technique span settings
The registry SHALL accept an optional `spans` block on `execution.adapter: technique_classifier` with `sentence_model`, `llm`, `timeout` (the span deadline), `max_sentences` (default 20), `max_phrases_per_technique` (default 5) and `max_input_chars` (default 20000). The block SHALL NOT be accepted on other adapters.

#### Scenario: Default caps
- **WHEN** a `spans` block with `timeout`, `sentence_model` and `llm` only is loaded
- **THEN** `max_sentences` is 20, `max_phrases_per_technique` is 5 and `max_input_chars` is 20000

#### Scenario: Spans on another adapter
- **WHEN** an `http_sync` analyzer sets `spans`
- **THEN** loading fails

### Requirement: Technique span sentence model settings
`spans.sentence_model` SHALL take `endpoint`, `headers`, `threshold` (required, 0 to 1) and `thresholds` (per-technique overrides), with `${VAR}` expansion of the endpoint and header values like the `technique` and `social` blocks.

#### Scenario: Override map
- **WHEN** `spans.sentence_model` has `threshold: 0.4` and `thresholds: {Doubt: 0.6}`
- **THEN** the effective threshold of `Doubt` is 0.6 and of every other technique 0.4

### Requirement: Technique span LLM settings
`spans.llm` SHALL take `base_url`, `api_key`, `model`, `prompt_file` (default `/etc/orchestrator/technique_spans/prompt.md`), `temperature`, `max_tokens` (default 1000), `response_format` (default `json_schema`), `headers` and `extra_body`, with `${VAR}` expansion as for the llm adapter. The prompt file SHALL be read once at load.

#### Scenario: Expansion
- **WHEN** an enabled analyzer sets `spans.llm` with `${VAR}` values and the variables are set
- **THEN** `base_url`, `api_key`, `model` and header values are expanded and the prompt is loaded

### Requirement: Technique span validation
Loading SHALL fail with an error naming the file, the analyzer id and the field when `spans` lacks `sentence_model`, `llm` or a positive `timeout`, when `spans.timeout` is not shorter than `execution.timeout`, when the sentence model has no endpoint, a missing `threshold` or one outside 0 to 1, a `thresholds` key that is not a technique name or a value outside 0 to 1, when a cap is not a positive integer or when `llm.extra_body` sets `model` or `messages`.

#### Scenario: Span deadline too long
- **WHEN** `execution.timeout` is 30s and `spans.timeout` is 30s
- **THEN** loading fails naming `execution.spans.timeout`

#### Scenario: Unknown technique override
- **WHEN** `spans.sentence_model.thresholds` has the key `Nope`
- **THEN** loading fails

### Requirement: Technique span LLM loading
For an enabled analyzer, loading SHALL fail, naming the field and never a value, when a variable in `spans` is unset, `spans.llm.base_url`, `api_key` or `model` is empty, `response_format` is unknown or the prompt file cannot be read.

#### Scenario: Empty api key
- **WHEN** an enabled analyzer's `spans.llm.api_key` expands to an empty string
- **THEN** loading fails naming `execution.spans.llm.api_key`

### Requirement: Technique classifier old keys
Loading SHALL fail, naming the file, the analyzer id and the replacement key, when an analyzer sets a key of the earlier technique classifier: `execution.short`, `execution.long`, `execution.short_max_words`, `execution.max_sentences`, or `threshold` or `thresholds` in the `technique` or `social` block.

#### Scenario: Old short and long blocks
- **WHEN** an analyzer sets `execution.short`, then `execution.long`
- **THEN** loading fails naming `execution.social`, then `execution.technique`

#### Scenario: Old threshold
- **WHEN** the `social` block sets `threshold: 0.3` or `thresholds`
- **THEN** loading fails with an error naming `execution.social.cutoff`

#### Scenario: Old word and sentence keys
- **WHEN** an analyzer sets `execution.short_max_words`, then `execution.max_sentences`
- **THEN** loading fails naming `social_max_words` and `technique_min_words`, then `spans.max_sentences`

### Requirement: Technique classifier span block kept
The `spans` block (`sentence_model` with `endpoint`, `headers`, `threshold` and `thresholds`, `llm`, `timeout` and the caps `max_sentences`, `max_phrases_per_technique`, `max_input_chars`) SHALL be accepted and validated as before this change. `threshold` and `thresholds` SHALL stay valid inside `spans.sentence_model`.

#### Scenario: Span block with new model blocks
- **WHEN** an analyzer with `technique` and `social` blocks has a `spans` block whose `sentence_model` sets `threshold: 0.4` and `thresholds: {Doubt: 0.6}`
- **THEN** it loads and the sentence model's effective threshold of `Doubt` is 0.6

#### Scenario: Span block still validated
- **WHEN** `spans.sentence_model.thresholds` has the key `Nope`
- **THEN** loading fails

### Requirement: Technique classifier NCI model blocks
Each of `technique` (the `nci-technique` model) and `social` (the `nci-social` model) SHALL have `endpoint` (required, non-empty, `${VAR}` expanded), `headers` (`${VAR}` expanded) and `cutoff` (optional, 0 to 1, default 0.5 for `technique` and 0.3 for `social`).

#### Scenario: Default cutoffs
- **WHEN** neither block sets `cutoff`
- **THEN** the effective cutoff is 0.5 for `technique` and 0.3 for `social`

#### Scenario: Configured cutoff
- **WHEN** `technique.cutoff` is 0.6
- **THEN** the effective cutoff of `nci-technique` is 0.6

### Requirement: Technique classifier bounds and cutoff validation
Loading SHALL fail with an error naming the file and the analyzer id when a model block is missing, an endpoint is empty, a `cutoff` is outside 0 to 1, `social_max_words` or `technique_min_words` is not positive, or `technique_min_words` is greater than `social_max_words + 1` (a word count would select no model).

#### Scenario: Cutoff out of range
- **WHEN** `technique.cutoff` is 1.5
- **THEN** loading fails naming the file, the analyzer `techniques` and `execution.technique.cutoff`

#### Scenario: Gap between the bounds
- **WHEN** `social_max_words` is 30 and `technique_min_words` is the default 40
- **THEN** loading fails naming `technique_min_words` and `social_max_words`

#### Scenario: Adjacent bounds
- **WHEN** `social_max_words` is 10 and `technique_min_words` is 11
- **THEN** the analyzer loads

### Requirement: Technique classifier example description
The example's `llm.description` SHALL state that a score is the model's confidence in that technique on the English translation, that several labels may be true at once, that a low score is not a technique, that scores of the two models are not comparable or averaged, and that `detected` entries have spans.

#### Scenario: Description
- **WHEN** the example's `llm.description` is read
- **THEN** it mentions the English translation, several labels at once, that a low score is not a technique, `nci-technique`, `nci-social` and spans

### Requirement: Mistral translation English skip settings
The `mistral_translation` execution SHALL accept `skip_english` (boolean, default `true`), `english_min_words` (integer >= 1, default `8`) and `english_min_ratio` (number > 0 and <= 1, default `0.25`). Invalid values SHALL fail loading with an error naming the file and analyzer id. These keys SHALL be rejected for every other adapter and are not subject to `${VAR}` expansion. Registry files without them SHALL load unchanged with the defaults.

#### Scenario: Invalid English skip settings
- **WHEN** `english_min_words` is below 1 or `english_min_ratio` is 0, negative or above 1
- **THEN** loading fails with an error naming the file and analyzer id

#### Scenario: English skip settings on another adapter
- **WHEN** an adapter other than `mistral_translation` sets `skip_english`, `english_min_words` or `english_min_ratio`
- **THEN** loading fails with an error naming the file and analyzer id
