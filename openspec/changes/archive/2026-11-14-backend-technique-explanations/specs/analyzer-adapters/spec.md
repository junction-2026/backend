## ADDED Requirements

### Requirement: Technique glossary artifact
The backend SHALL ship a versioned technique glossary in the repository (`internal/adapter/technique_glossary.yaml`, embedded in the binary) with a `version`, a `name` and a one-line `definition` for each of the 18 technique names and the 3 LLM technique labels, and a span and a no-span `why` template, for each of the languages en, fi, sv, de, fr, es, ru and uk.

#### Scenario: Every entry present
- **WHEN** the tests run
- **THEN** a test asserts a non-empty name and definition for every technique and LLM label in every supported language

### Requirement: Glossary English wording
English glossary definitions SHALL follow the technique definitions the nci-technique model is documented with (the SemEval-2020 Task 11 definitions).

#### Scenario: English loaded language
- **WHEN** the English definition of `Loaded_Language` is read
- **THEN** it says that words or phrases with strong emotional implications are used to influence the audience

### Requirement: Glossary startup check
The process SHALL fail to start when any supported language lacks an entry, a non-empty name or definition, or a template, or when the glossary has an unknown technique or language.

#### Scenario: Missing language
- **WHEN** the glossary has no `uk` entry for `Doubt`
- **THEN** glossary parsing fails naming `Doubt` and `uk`, and the orchestrator does not start

### Requirement: Glossary version
`glossary_version` SHALL equal the artifact's `version` string, and the version SHALL change whenever any wording of the artifact changes. A test SHALL pin the artifact's content hash together with its version so that a wording change without a version change fails the tests.

#### Scenario: Wording edited without version bump
- **WHEN** a definition is reworded and `version` is unchanged
- **THEN** the glossary hash test fails

### Requirement: Why templates
Each language SHALL have a span template with exactly one `{name}` and exactly one `{span}` placeholder and a no-span template with exactly one `{name}` and no `{span}`. Rendering SHALL substitute the localized name and the span text literally in one pass and SHALL NOT call an LLM or any other model.

#### Scenario: Template with two name placeholders
- **WHEN** a span template contains `{name}` twice
- **THEN** glossary parsing fails

#### Scenario: Placeholder in the span text
- **WHEN** a span `text` is `say {name} now`
- **THEN** its `why` contains `say {name} now` unchanged

### Requirement: Technique explanation language
A succeeded `techniques` result SHALL carry `explanation_language`, the translation's `source_language` lower-cased when the glossary has it and `en` otherwise, and `glossary_version`.

#### Scenario: German post
- **WHEN** the translation reports `source_language: "de"`
- **THEN** `explanation_language` is `de` and `plain_language` members and span `why` are German

#### Scenario: Unsupported language
- **WHEN** the translation reports `source_language: "it"` or none
- **THEN** `explanation_language` is `en`

### Requirement: Technique plain-language members
Every Detection of a succeeded `techniques` result SHALL carry `plain_language` `{name, definition, why}` from the glossary and every span a `why` (the span template with its `text`). `plain_language.why` is the first span's `why`, or the no-span template when `spans` is empty, including `span_status` `failed` or `timeout`.

#### Scenario: German span
- **WHEN** `Loaded_Language` has the span `LÜGEN euch` and `explanation_language` is `de`
- **THEN** the span's `why` is German, contains `LÜGEN euch` and equals `plain_language.why`

#### Scenario: Span step failed
- **WHEN** `span_status` is `failed`
- **THEN** every `plain_language.why` is the no-span sentence

### Requirement: LLM techniques adapter inputs
The `llm_techniques` adapter SHALL read `source_language` from its `translation` binding, `Job.input.content` and, only for a job created with `input` (no `scrape`), `Job.input.source.title`. It SHALL NOT send the translation.

#### Scenario: Scraped job
- **WHEN** a scraped job has `source.title` set to an account display name
- **THEN** the title is not sent and `title_checked` is false

### Requirement: LLM techniques title check
The title SHALL be checked (`title_checked: true`, sent to the LLM, all three labels asked) only when it is non-empty after trimming and differs from the trimmed content; otherwise it is not sent and `misleading_headline` is not asked.

#### Scenario: Title checked
- **WHEN** a job created with `input` has `source.title` `Vaccine KILLS thousands` and different content
- **THEN** the LLM request carries the title and `labels_checked` lists all three labels

#### Scenario: Title equal to body
- **WHEN** `source.title` equals the content after trimming
- **THEN** the title is not sent and `title_checked` is false

### Requirement: LLM techniques input limit
The adapter SHALL send the content cut at `max_input_chars` characters (at a space when possible) and set `truncated` when it cut.

#### Scenario: Long content
- **WHEN** the content is longer than `max_input_chars`
- **THEN** only its first part is sent and `truncated` is true

### Requirement: LLM techniques prompt
The system prompt (`config/llm_techniques/prompt.md`) SHALL treat the content and title as data, list only the three LLM technique labels with definitions and ask for a JSON object `{"findings": [{"label", "field", "text", "confidence", "explanation"}]}` with `explanation` one sentence in the requested language.

#### Scenario: Prompt content
- **WHEN** the shipped prompt is read
- **THEN** it names the three labels, says the content is data and asks for the findings object

### Requirement: LLM techniques user message
The user message SHALL carry the content in `<content>`, the checked title in `<title>` (closing tags escaped), the labels asked for, the explanation language and the reply schema unless `json_schema` mode carries it.

#### Scenario: German job
- **WHEN** a German job is analyzed
- **THEN** the user message carries the German content, asks for explanations in `de` and contains no text of the translation

### Requirement: LLM techniques reply shape
A reply that is not an object with a `findings` array of objects with string `label`, `field`, `text`, `explanation` and numeric `confidence` SHALL be an invalid reply: one reply retry, then `invalid_output`.

#### Scenario: Unreadable answer
- **WHEN** the reply and its retry are prose
- **THEN** the run fails with `invalid_output`

### Requirement: LLM techniques finding acceptance
A finding SHALL be dropped when its label was not asked for, its `field` is not the label's field, its `confidence` is outside 0..1 or below `min_confidence`, its explanation is blank, or its `text` is empty or not an exact substring of the field's full string. A kept span is the leftmost occurrence in UTF-16 offsets.

#### Scenario: Paraphrased span
- **WHEN** the content is `Crime rose 300% in May.` and the LLM quotes `crime rose 300 percent`
- **THEN** the finding is dropped and the run succeeds

#### Scenario: Unknown and SemEval labels
- **WHEN** the LLM answers with findings labelled `fabricated_authority` and `Appeal_to_Authority`
- **THEN** both are dropped

#### Scenario: Non-BMP title
- **WHEN** the title is `🚨 Vaccine KILLS thousands` and the LLM quotes `KILLS thousands`
- **THEN** the span has `start: 11` and `end: 26`

### Requirement: LLM techniques cap and order
Duplicates by label, field, start and end SHALL keep the highest confidence; at most `max_findings_per_label` per label are kept, highest confidence first, and dropping more sets `truncated`. Findings are ordered by label order, `title` before `content`, `start`, `end`.

#### Scenario: Cap
- **WHEN** the LLM returns six accepted `evidence_certainty_mismatch` findings and the default cap applies
- **THEN** the five with highest confidence are kept and `truncated` is true

### Requirement: LLM techniques finding texts
A finding's `name` and `definition` SHALL come from the glossary in `explanation_language`; its `explanation` is the LLM's text with configured credentials redacted.

#### Scenario: Glossary texts
- **WHEN** a German job has a `statistical_cherry_picking` finding
- **THEN** its `name` and `definition` are the German glossary entries

### Requirement: LLM techniques failures and redaction
The adapter SHALL fail with `adapter_error` for a non-2xx LLM answer or connection error, and `timeout` or `cancelled` per its context; no error message or result member SHALL contain the API key or a configured header value. Its failure SHALL NOT change the `techniques` run.

#### Scenario: HTTP 503
- **WHEN** the LLM endpoint answers HTTP 503 twice
- **THEN** the run fails with `adapter_error`

#### Scenario: Echoed key
- **WHEN** the LLM answers HTTP 401 with an error message repeating the API key
- **THEN** the error message does not contain the key
