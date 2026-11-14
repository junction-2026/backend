## ADDED Requirements

### Requirement: wikipedia coverage computation
The wikipedia adapter SHALL compute each WikiLookup's `coverage` and `evidence_sentence` (shared `wikipedia-lookup-result` "Wikipedia coverage rule order") with a pure function of the claim and that WikiLookup in `internal/adapter/wikipedia_coverage.go`, called from `personalize` after `matched_entities` and `matched_numbers` are set, for fresh answers, cache hits and in-run duplicates alike. A lookup whose `status` is not `found` SHALL get `no_coverage` and a null `evidence_sentence`.

#### Scenario: Cache hit relabelled for the current claim
- **WHEN** a cached `found` answer for `en|Angela Merkel` with passage "Angela Merkel is a German politician who was Chancellor of Germany from 2005 to 2021. She led the CDU." serves the claim "Angela Merkel became chancellor in 2006."
- **THEN** the cached WikiLookup has the same `coverage: "conflicts"` and `evidence_sentence` as the uncached one, and serving the claim "Angela Merkel became chancellor in 2005." from the same cache entry gives `aligned`

#### Scenario: No page
- **WHEN** a lookup has `status: "no_page"` or `status: "ambiguous"`
- **THEN** it has `coverage: "no_coverage"` and `evidence_sentence: null`

### Requirement: wikipedia passage sentence splitting
The adapter SHALL split `passage` after each `.`, `!`, `?`, `…`, `。`, `！` or `？` that is followed by whitespace or the end of `passage`, and at each line break (`\n`, `\r`); it SHALL trim each part and drop empty parts, so that every sentence is a substring of `passage`.

#### Scenario: Mixed punctuation and line breaks
- **WHEN** `passage` is "  Es kostet 2.5 Euro! Wirklich?\r\n\nJa… 东京。 大阪！"
- **THEN** the sentences are "Es kostet 2.5 Euro!", "Wirklich?", "Ja…", "东京。" and "大阪！"

### Requirement: wikipedia coverage numbers
The adapter SHALL use as numbers the maximal ASCII digit runs, as for `matched_numbers`; a run of four digits SHALL be a year and any other run a count. It SHALL NOT normalize number words or date formats. A digit run directly preceded or followed by `.`, `,`, `'`, U+00A0 or U+202F that is itself adjacent to another digit SHALL never be the differing number of `conflicts`.

#### Scenario: Grouped number never conflicts
- **WHEN** the claim is "Die Stadt hat 1500 Einwohner." with entity `Stadt` and the passage is "Die Stadt hat 1.500 Einwohner."
- **THEN** the lookup is `mentioned`

#### Scenario: Number words are not numbers
- **WHEN** the claim is "The pension age is 70." and the passage says "The pension age is seventy."
- **THEN** the lookup is `mentioned`

### Requirement: wikipedia coverage downgrade
Before it emits `aligned` or `conflicts` the adapter SHALL re-check that `evidence_sentence` is a non-empty substring of `passage` that contains every claim number (`aligned`) or the differing number (`conflicts`), and SHALL emit `mentioned` with a null `evidence_sentence` otherwise.

#### Scenario: Span not in passage
- **WHEN** the candidate span is not a substring of `passage` or lacks the needed number
- **THEN** the lookup is `mentioned` with `evidence_sentence: null`

### Requirement: wikipedia llm description
The wikipedia analyzer's shipped `llm.description` (`docs/examples/wikipedia.yaml`) SHALL name the coverage labels and state that an empty list, `no_page`, `ambiguous` or `no_coverage` means the claim was not found in Wikipedia, not that it is false, that same-day events are expected to be `no_coverage`, and that `news_search` is where reporting shows.

#### Scenario: Description read by the finalizer
- **WHEN** the finalizer receives the wikipedia entry
- **THEN** its description says that `no_coverage` means not found in Wikipedia, not false

### Requirement: wikipedia coverage tests
The adapter tests SHALL be table-driven and cover each label, the downgrade rule, grouped and decimal numbers never conflicting, a today-event claim giving `no_coverage` or `mentioned`, and cache-hit parity.

#### Scenario: Test suite
- **WHEN** `go test ./internal/adapter` runs
- **THEN** the coverage tests pass
