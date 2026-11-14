## ADDED Requirements

### Requirement: wikipedia date recognition
The wikipedia adapter SHALL recognise in a passage, as `date` values, ISO 8601 `YYYY-MM-DD` and `YYYY-MM` for every language; for `de` also `D.M.YYYY`, `D. Monat YYYY` and `Monat YYYY`; for `en` also `Month D, YYYY`, `D Month YYYY` and `Month YYYY`. Any other language SHALL recognise ISO 8601 only. A date value is `YYYY-MM-DD` or `YYYY-MM`, and the digits of a recognised date SHALL NOT also give a `number` value.

#### Scenario: Same date in two languages
- **WHEN** a `de` passage contains "2. März 2025" and an `en` passage contains "2 March 2025"
- **THEN** both give the value `{"kind": "date", "value": "2025-03-02"}`

#### Scenario: Month and year
- **WHEN** a `de` passage contains "März 2025"
- **THEN** it gives the value `{"kind": "date", "value": "2025-03"}` and no number

#### Scenario: Other language
- **WHEN** a `fr` passage contains "2 mars 2025" and "2025-03-02"
- **THEN** only `2025-03-02` is a date; the digits of "2 mars 2025" are numbers

### Requirement: wikipedia number recognition
The adapter SHALL recognise every other digit run as a `number`, read with the separators of the wiki language: `de` groups with `.`, U+00A0 or U+202F and has `,` as decimal separator; `en` groups with `,` and has `.` as decimal separator; any other language uses the `en` separators plus U+00A0 and U+202F grouping. The `value` SHALL be plain decimal: no group separators, `.` as decimal separator, no leading zeros except a single `0`, no trailing fractional zeros. A standalone year is a number.

#### Scenario: Grouped numbers
- **WHEN** a `de` passage contains "1.500" and an `en` passage contains "1,500"
- **THEN** both give `{"kind": "number", "value": "1500"}`

#### Scenario: Decimal comma and year
- **WHEN** a `de` passage contains "2,50 Euro seit 2012"
- **THEN** it gives the numbers `2.5` and `2012`

### Requirement: wikipedia comparison values
The adapter SHALL list the values of a passage deduplicated by (kind, value), in order of first appearance, each with `text` as the verbatim passage substring at its first occurrence.

#### Scenario: Repetition
- **WHEN** a passage mentions "67" twice and "67,0" once
- **THEN** it has one value `67` with `text` "67"

### Requirement: wikipedia cross-language comparison
The adapter SHALL compute `cross_language` and `cross_language_differences` of each ClaimLookup with a pure function of its WikiLookups' `status`, `passage`, `language` and `url` in `internal/adapter/wikipedia_crosslang.go`, called once after every WikiLookup (and its `coverage`) is set. It SHALL NOT read or change `coverage`, `evidence_sentence` or any other WikiLookup member.

#### Scenario: Agree
- **WHEN** both WikiLookups are `found` with the same value sets, or with no values
- **THEN** `cross_language` is `agree` and `cross_language_differences` is `null`

#### Scenario: Discrepancy
- **WHEN** both are `found` and the value sets differ
- **THEN** `cross_language` is `discrepancy` and `cross_language_differences` has one entry per WikiLookup in `languages` order with the values found only in that passage (possibly an empty array)

#### Scenario: Gap, single wiki, none found
- **WHEN** exactly one of two WikiLookups is `found`, or the claim has one WikiLookup, or none is `found`
- **THEN** `cross_language` is `gap` for the first case and `null` otherwise, and `cross_language_differences` is `null`

#### Scenario: Cached rerun
- **WHEN** every WikiLookup is answered from the cache
- **THEN** the members equal those of the first run and `coverage`, `evidence_sentence` and passages are unchanged
