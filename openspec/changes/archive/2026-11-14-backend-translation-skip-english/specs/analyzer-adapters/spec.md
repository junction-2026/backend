## MODIFIED Requirements

### Requirement: Mistral translation result members
The `mistral_translation` analyzer result SHALL be an object with at least `translated_text`, `source_language`, `model` (the response's `model`, else the configured one), `truncated` and `input_characters`. `usage` (the provider's usage object or `null`) SHALL be present only when Mistral was called, and `skipped` only when it was not; other members of the reply are dropped.

#### Scenario: Result members
- **WHEN** the reply has no `model`, a non-string `source_language` and extra members
- **THEN** `model` is the configured model, `source_language` is `null`, the extra members are absent, `usage` is present and `skipped` is absent

## ADDED Requirements

### Requirement: Mistral translation English skip
The `mistral_translation` adapter SHALL compute the text to send (the trimmed-empty check plus the `max_input_chars` cut) before deciding anything; empty input SHALL still fail with `adapter_error` and make no request. When `skip_english` is true it SHALL then run the English check on exactly that text before any HTTP request.

#### Scenario: Empty input
- **WHEN** `normalized_content` is empty or only whitespace
- **THEN** the adapter fails with `adapter_error` and Mistral receives no request

#### Scenario: Check runs on the cut text
- **WHEN** English content is longer than `max_input_chars`
- **THEN** the check sees the cut text, `translated_text` is that cut text, `truncated` is true and `input_characters` is the original length in code points

### Requirement: Mistral translation skipped result
On `english` the adapter SHALL send no HTTP request and return a result with at least `translated_text` (exactly the text that would have been sent, not trimmed or altered), `source_language: "en"`, `model: "none"`, `skipped: "detected_english"`, `truncated` and `input_characters`, and no `usage` member. The AnalyzerRun SHALL be `succeeded`, not `skipped`.

#### Scenario: English text
- **WHEN** the text is `They are LYING to you! Only we tell the truth. Wake up before it's too late, patriots!`
- **THEN** Mistral receives no request, the result has `translated_text` equal to that text, `source_language` `en`, `model` `none`, `skipped` `detected_english`, `truncated` false, `input_characters` and no `usage`, and the AnalyzerRun is `succeeded`

#### Scenario: Consumers of a skipped result
- **WHEN** `techniques` and `propaganda_detection` are bound to a skipped result
- **THEN** they receive `translated_text` and `source_language` `en` as for a Mistral result, and `techniques` uses `span_method` `sentence` with offsets into `Job.input.content`

### Requirement: Mistral translation without skip
On `not_english` or `unsure`, or when `skip_english` is false, the adapter SHALL send exactly one request and produce the result it produces today. A result produced by Mistral SHALL NOT carry `skipped`.

#### Scenario: German or unsure text
- **WHEN** the check says `not_english` or `unsure`
- **THEN** exactly one request is sent and the result is the Mistral result without `skipped`

#### Scenario: Skip disabled
- **WHEN** `skip_english` is false and the text is English
- **THEN** exactly one request is sent

### Requirement: Mistral translation English check words
The English check SHALL be a pure function (no I/O, model, service or new module dependency) of the text, `english_min_words` and `english_min_ratio`, returning `english`, `not_english` or `unsure`. It SHALL lower-case the text, remove URLs (`http://`, `https://`, `www.` tokens), `@mentions` and `#hashtags`, and take as words the maximal runs of Unicode letters and apostrophes (`'` and `’`), discarding runs without a letter. Digits, emoji and punctuation are not words.

#### Scenario: Too short
- **WHEN** the text has fewer than `english_min_words` words, such as `BREAKING!!! 🚨 #EU`, `OK, deal, Brexit Boris Johnson` or a URL-only text
- **THEN** the check returns `unsure`

### Requirement: Mistral translation English check script
After the word count test, the check SHALL return `not_english` when more than 10% of the letters in the words are not Latin script (`unicode.Latin`).

#### Scenario: Non-Latin text
- **WHEN** the text is a Russian, Ukrainian or Greek sentence of at least `english_min_words` words
- **THEN** the check returns `not_english`

#### Scenario: Short non-Latin text
- **WHEN** a Russian text has fewer than `english_min_words` words
- **THEN** the check returns `unsure` and Mistral is called

### Requirement: Mistral translation English check word lists
The check SHALL use a fixed English list of about 100 common function words and fixed lists of about 50 function words each for German, French, Spanish, Italian, Dutch and Portuguese. A language's share SHALL be the number of words in its list divided by the total word count; a word in several lists counts for each.

#### Scenario: Shared word
- **WHEN** a word is in the English and the Portuguese list
- **THEN** it counts for both shares

### Requirement: Mistral translation English check decision
The check SHALL return `english` only when the English share is at least `english_min_ratio` and at least 2 × the highest other-language share; otherwise `not_english` when some other language's share is at least 0.20 and greater than the English share; otherwise `unsure`. It SHALL be biased toward calling Mistral: in doubt it returns `unsure`, never `english`.

#### Scenario: English examples
- **WHEN** the text is `The city council meets on Tuesday at 6pm to discuss the new bike lanes.` or a 100-word English paragraph
- **THEN** the check returns `english`

#### Scenario: Not English examples
- **WHEN** the text is a German, French or Spanish sentence of at least 8 words
- **THEN** the check returns `not_english`

#### Scenario: Mixed text
- **WHEN** the text is a German sentence with an English quote where neither language clearly dominates
- **THEN** the check returns `unsure`
