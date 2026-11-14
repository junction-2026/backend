# Tasks

## 1. Registry
- [x] 1.1 `term_context` adapter constant, keys, defaults and validation; Wikipedia transport shared with `wikipedia`
- [x] 1.2 Registry tests (defaults, rejected keys, dependencies, bindings)

## 2. Adapter
- [x] 2.1 `LLM` variant with explicit prompt and schema; selection and comparison prompts and schemas
- [x] 2.2 Selection, verbatim term matching, de-duplication, cap and order
- [x] 2.3 `in_post` sentence extraction, `elsewhere` snippet matching
- [x] 2.4 Own-page lookup, lead sentence, permalink, cache including `no_coverage`
- [x] 2.5 Comparison, `sense`, `differs_from`, `unknown` without a call, failures and credential hiding
- [x] 2.6 Activity facts and text
- [x] 2.7 Unit tests, example registry entry, README

## 3. Validation
- [x] 3.1 `go test ./...`, `scripts/validate-openspec.sh`
