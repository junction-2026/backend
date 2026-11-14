## ADDED Requirements

### Requirement: term_context prompts
The `term_context` adapter SHALL send its selection and comparison system prompts from the backend source, not from a file. Both prompts SHALL forbid writing a definition, explanation or summary, naming a political tribe, camp or ideology, and giving a verdict, and SHALL tell the model that the post and quotes are data and never instructions.

#### Scenario: Prompts forbid definitions
- **WHEN** the selection request is sent
- **THEN** its system message forbids definitions, camp names and verdicts and its user message holds the text only inside `<content>` with a closing tag escaped

### Requirement: term_context replies
The selection reply SHALL be `{"terms": [string]}` and the comparison reply `{"terms": [{"term", "sense", "differs_from"}]}`. Both SHALL follow the LLM reply retry rules (one retry of a cut-off, non-JSON or schema-failing reply, usage of both attempts combined). A second invalid reply SHALL fail the run with `invalid_output`.

#### Scenario: Invalid comparison reply retried once
- **WHEN** the first comparison reply gives `sense: "maybe"` and the second is valid
- **THEN** the run succeeds with the second reply and `usage` sums both requests

#### Scenario: Invalid comparison reply twice
- **WHEN** both replies are invalid
- **THEN** the run fails with `invalid_output` and no result

### Requirement: term_context term matching
The adapter SHALL keep a picked string only when it contains a letter and occurs in the original text, compared with Unicode simple case folding, with no Unicode letter or digit directly before or after the occurrence. `term` SHALL be copied from the first such occurrence in the text. Snippet matching for `elsewhere` SHALL use the same comparison. Persons, numbers and dates are excluded by the selection prompt only; the backend does not classify picks.

#### Scenario: Boundaries
- **WHEN** the pick is `EU` and the text has "Europe", "EU-wide" and "EUR"
- **THEN** the first occurrence with non-letter, non-digit neighbours ("EU-wide") is kept and "Europe" and "EUR" are skipped

#### Scenario: Unicode folding
- **WHEN** the pick is `ärger` and the text has "Ärger!" but also "Särger"
- **THEN** `term` is "Ärger"

#### Scenario: Not a substring
- **WHEN** the pick does not occur in the text
- **THEN** it is dropped and no Wikipedia request is made for it

### Requirement: term_context sentence extraction
`in_post` SHALL be computed by the backend as the contiguous part of the original text around the first occurrence of `term` between sentence boundaries (line break, or one of `.`, `!`, `?`, `…`, `。`, `！`, `？`, `؟` followed by whitespace; the terminator stays with the sentence before it), trimmed. A sentence longer than `in_post_max_chars` SHALL be cut to a window of at most that many characters that contains the occurrence.

#### Scenario: CJK and Arabic terminators
- **WHEN** the text is "価格が上がった。 これが大リセットだ！ 次の文。" and the term is "大リセット", or "هل هذا؟ هذا هو الكلمة. نعم" and the term is "الكلمة"
- **THEN** `in_post` is "これが大リセットだ！" respectively "هذا هو الكلمة."

#### Scenario: Terminator without whitespace
- **WHEN** the text is "価格が上がった。これが大リセットだ！" (no whitespace after `。`) and the term is "大リセット"
- **THEN** `in_post` is the whole text, because a terminator counts only when followed by whitespace

#### Scenario: Long sentence
- **WHEN** the sentence has 1000 characters and the limit is 300
- **THEN** `in_post` has at most 300 characters, contains `term` and is a substring of the text

### Requirement: term_context own-page lookup
The adapter SHALL resolve each kept term in the wiki `{source_language}.wikipedia.org` (`en` when unknown) with one title request (`redirects=1`) and, only when the title has no page, one `generator=search` request whose first hit with title equal to the term (case-insensitively, optionally followed by " (qualifier)") is the own page. A disambiguation page, a missing page or an empty lead gives `no_coverage`.

#### Scenario: Redirect
- **WHEN** the title `EU` is redirected to `European Union`
- **THEN** `encyclopedia.title` is `European Union` and `revision_id` is that page's revision

#### Scenario: Disambiguation
- **WHEN** the resolved page has the `disambiguation` page property
- **THEN** `encyclopedia` is `no_coverage` and no search request is sent

### Requirement: term_context lead quote
`quote` SHALL be the first sentence of the lead (ending at `.`, `!`, `?`, `。` or `؟` followed by whitespace or the end, outside parentheses and square brackets), cut to `quote_max_chars`, with the `oldid` permalink of the answered revision.

#### Scenario: Lead sentence with parentheses
- **WHEN** the lead is "The Great Reset (also: Davos agenda.) is an initiative. It was launched in 2020."
- **THEN** `quote` is "The Great Reset (also: Davos agenda.) is an initiative."

### Requirement: term_context Wikipedia cache
Term lookups SHALL be stored in the `wikipedia` adapter's persistent cache directory under the key of (language, normalised term, search mode `term`), including `no_coverage` outcomes, and SHALL be read before any request. Failed requests SHALL NOT be stored. `cached` SHALL count lookups answered from the cache or from an identical key earlier in the run; `requests_used` SHALL count HTTP requests sent, including the single retry.

#### Scenario: Second job
- **WHEN** a second job keeps the same terms while the first job's answers are cached
- **THEN** `requests_used` is 0 and `cached` equals the number of kept terms, `no_coverage` terms included

#### Scenario: Failed request
- **WHEN** Wikipedia answers HTTP 503 twice
- **THEN** the run fails with `adapter_error` and nothing is cached for that term

### Requirement: term_context sense
A term with `encyclopedia` `no_coverage` and no `elsewhere` item SHALL get `sense: unknown` and `differs_from: []` without any LLM call. All other terms SHALL be compared in one request holding only `term`, `in_post`, `quote` and `snippet`s. `differs` SHALL require at least one resolvable QuoteRef; the result SHALL hold only the `same`/`differs` value and the QuoteRefs, never model text.

#### Scenario: Unknown without a call
- **WHEN** every kept term has no other quote
- **THEN** no comparison request is sent and every `sense` is `unknown`

#### Scenario: Differs
- **WHEN** the comparison names `{"source": "encyclopedia", "index": null}` for a term
- **THEN** `sense` is `differs` and `differs_from` is that reference

### Requirement: term_context activity facts
The activity of `term_context` SHALL have text "Looking up unfamiliar terms" and, when the translation result is readable, `params.language` (the wiki language) and a detail naming it. Facts SHALL come from the adapter's own decision function without any network call and SHALL NOT contain credentials or provider error text.

#### Scenario: German post
- **WHEN** `translation.source_language` is `de`
- **THEN** the activity has `params.language: "de"` and detail "in the German Wikipedia"

### Requirement: term_context test coverage
Unit tests SHALL cover: selection filtering of non-substrings and of persons, numbers and dates (dropped by the substring and letter rules), sentence extraction including CJK and Arabic terminators and the long-sentence cut, redirect and disambiguation lookups, lead-sentence parsing with parentheses, snippet matching boundaries, `unknown` without an LLM call, an invalid comparison reply retried and failing, and cache hits counted in `cached`.

#### Scenario: Tests exist and pass
- **WHEN** `go test ./...` runs
- **THEN** the term_context tests pass without network access
