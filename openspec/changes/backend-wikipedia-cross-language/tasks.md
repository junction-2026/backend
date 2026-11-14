# Tasks

## 1. Extractor
- [x] 1.1 `wikipedia_crosslang.go`: date and number recognition per language, normalized values, dedup by (kind, value)
- [x] 1.2 Table-driven tests (de/en dates, grouped numbers, decimal comma, ISO dates, year-only, no digits)

## 2. Comparison
- [x] 2.1 Pure `crossLanguage` from the WikiLookups; `claimLookup` members set after coverage
- [x] 2.2 Adapter tests: agree, agree without values, discrepancy (one side empty), gap, single wiki, both missing, cached rerun, coverage unchanged
