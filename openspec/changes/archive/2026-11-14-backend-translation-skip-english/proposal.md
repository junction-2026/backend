# Proposal: Skip the Mistral translation call for clearly English text

## Why

Parent feature junction-2026/project#23. The `mistral_translation` adapter pays a Mistral round trip, delay and
token cost even for text that is already English. A pure-Go check on the text that would be sent can answer
that locally, so only text that is not clearly English reaches Mistral.

## What Changes

- New `internal/adapter/translation_english.go`: a pure function returning `english`, `not_english` or `unsure`
  from fixed function-word lists (English plus German, French, Spanish, Italian, Dutch, Portuguese) and a script test.
- `mistral_translation` runs it on the exact text to send; on `english` it returns the text unchanged with
  `source_language: "en"`, `model: "none"`, `skipped: "detected_english"` and no `usage`, without any HTTP request.
- New `mistral_translation` settings `skip_english` (default true), `english_min_words` (8), `english_min_ratio` (0.25).
- `docs/examples/propaganda_detection.yaml` gets a comment line for `skip_english`.
- Tests: table-driven check, adapter tests with a counting Mistral stub, registry tests, one engine test with
  `techniques` and `propaganda_detection` bound to a skipped result.

## Shared specs

No shared-specs change (pinned `junction-2026/shared-specs@2bda20ae4aebdb404c7a3035ed9d31df44d5ea54`): the
translation result shape is backend-local, and shared consumers read only `translated_text` and
`source_language`, which keep their meaning.

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `analyzer-adapters`: English check, skipped result, result members.
- `analyzer-registry`: `mistral_translation` settings.
