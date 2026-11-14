## ADDED Requirements

### Requirement: Technique spans without configuration
Without a `spans` block the `technique_classifier` adapter SHALL send no extra request and still return the span members of shared change `technique-spans`: `span_method` by language (`sentence` for `source_language` `en`, else `phrase`), `span_status` `ok`, `spans_truncated` false and `spans: []` on every Detection of `detected`.

#### Scenario: Feature off
- **WHEN** a German job detects `Loaded_Language` and the analyzer has no `spans` block
- **THEN** `span_method` is `phrase`, `span_status` is `ok` and `Loaded_Language` has `spans: []`

### Requirement: Technique span sentences
For `span_method` `sentence` the adapter SHALL split `Job.input.content` (not `normalized_content`, not the translation) with the technique sentence rules (`.`, `!`, `?`, newlines; fragments under 3 words dropped), with UTF-16 offsets into the original. Only the first `spans.max_sentences` sentences SHALL be scored and `spans_truncated` SHALL be true when more existed.

#### Scenario: Offsets after an emoji
- **WHEN** the content is `"Wake up now 😡! They are LYING to you."`
- **THEN** the sentence `"They are LYING to you."` has `start` 16 and `end` 38

#### Scenario: Sentence cap
- **WHEN** the content has 3 sentences and `max_sentences` is 2
- **THEN** 2 sentences are scored and `spans_truncated` is true

### Requirement: Technique span sentence model request
The adapter SHALL send one `POST {"text": [sentence 1..N]}` with the sentence model's headers and validate the answer like the document answer (18 labels per list, scores 0 to 1, one list per sentence). A sentence SHALL be a span of each technique in `detected` it scores at or above the sentence model's effective threshold for that technique; scores of other techniques SHALL be ignored. A non-2xx answer, a connection error or any validation failure SHALL give `span_status` `failed`.

#### Scenario: Undetected technique
- **WHEN** a sentence scores `Doubt` 0.95 and `Doubt` is not in `detected`
- **THEN** no `Doubt` span exists and `detected` is unchanged

#### Scenario: Override threshold
- **WHEN** the sentence model's `Name_Calling,Labeling` override is 0.6 and a sentence scores it 0.55
- **THEN** that sentence is not a span of `Name_Calling,Labeling`

### Requirement: Technique span LLM request
For `span_method` `phrase` the adapter SHALL send one chat-completions request with the system prompt from `spans.llm.prompt_file` and a user message holding the content in a `<content>` block with its closing tag escaped, followed by one JSON line per detected technique with `name`, `display` and `explanation`. Content longer than `max_input_chars` characters SHALL be cut at a space and `spans_truncated` SHALL be true.

#### Scenario: Request never carries the translation
- **WHEN** a German job is analyzed
- **THEN** the user message contains the original content and not `translated_text`

### Requirement: Technique span LLM reply
The reply SHALL be `{"phrases": [{"technique": string, "text": string}]}` (schema `technique_spans` in `json_schema` mode). Any other reply, a non-2xx answer or a connection error SHALL give `span_status` `failed`.

#### Scenario: Unreadable reply
- **WHEN** the LLM answers with text that is not a JSON object
- **THEN** `span_status` is `failed` and every `detected[].spans` is empty

### Requirement: Technique span candidate acceptance
A candidate SHALL be accepted only if it is non-empty and an exact byte-for-byte substring of `Job.input.content`; its span SHALL be the leftmost occurrence with UTF-16 offsets. Duplicate candidates of one technique SHALL collapse; candidates with unknown or undetected technique names SHALL be ignored; at most `spans.max_phrases_per_technique` candidates SHALL be accepted per technique and `spans_truncated` SHALL be true when more were dropped.

#### Scenario: Rejected variants
- **WHEN** the content is `"Sie LÜGEN euch an. 😡 Sie LÜGEN euch an!"` and the LLM returns `"They are LYING to you."`, `"lügen euch"`, `"LÜGEN  euch"` and `"LÜGEN euch"` for `Loaded_Language`
- **THEN** `Loaded_Language` has one span from these, `{text: "LÜGEN euch", start: 4, end: 14}`

#### Scenario: All candidates rejected
- **WHEN** every candidate of a detected technique differs in case from the content
- **THEN** it stays in `detected` with `spans: []`

### Requirement: Technique span ordering
The spans of each Detection SHALL be sorted by `start`, then `end`, without two spans with the same `start` and `end`.

#### Scenario: LLM order differs
- **WHEN** the LLM returns a later phrase before an earlier one
- **THEN** the earlier phrase's span comes first

### Requirement: Technique span deadline
The span step SHALL run under its own deadline `spans.timeout` inside the analyzer's context. When it elapses `span_status` SHALL be `timeout`. A failed or timed-out span step SHALL leave the run `succeeded` with every `detected[].spans` `[]` and `techniques`, `detected` membership and scores, `sentences` and `offsets_in` equal to the result without spans. When the analyzer's own timeout elapses the run SHALL fail with `timeout` as before. No span request SHALL be sent when `detected` is empty.

#### Scenario: Slow LLM
- **WHEN** the span LLM does not answer within `spans.timeout`
- **THEN** the run succeeds with `span_status` `timeout`

#### Scenario: Analyzer timeout during the span step
- **WHEN** the analyzer's context ends while the span LLM is waiting
- **THEN** the run fails with `timeout`

### Requirement: Technique span errors and credentials
Span errors SHALL NOT be put into the result; the adapter SHALL log the cause once with the sentence model's header values and the span LLM's api_key and header values redacted. Neither the result nor logs SHALL contain them, even when a service echoes them.

#### Scenario: Echoed key
- **WHEN** the span LLM answers 401 with an error message that repeats its api_key
- **THEN** `span_status` is `failed` and neither the result nor the log contains the key

### Requirement: Technique span tests
Span tests SHALL use httptest stubs only and cover UTF-16 offsets with non-ASCII and non-BMP content, both methods, thresholds and overrides, candidate rejection, caps, the empty-detected case, the feature-off case, 503, connection refused, invalid bodies, the span deadline, the analyzer timeout and credential redaction.

#### Scenario: Suite runs offline
- **WHEN** `go test ./...` runs without network access
- **THEN** the span tests pass
