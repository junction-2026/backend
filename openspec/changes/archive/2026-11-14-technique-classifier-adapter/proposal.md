# Proposal: technique_classifier adapter

## Why

Parent feature junction-2026/project#15, child issue junction-2026/backend#51. The backend gets an analyzer
`techniques` that scores the English translation of a job's text for the 18 SemEval-2020 Task 11 propaganda
techniques and finds the sentences that carry them. A short-text model (BERTweet) or a long-text model
(ModernBERT) is chosen by word count; both are third-party ML services that are not registered repositories, so
their request and response contract is backend-local.

## What Changes

- New adapter `technique_classifier` (`internal/adapter/technique.go`, `technique_split.go`, `technique_names.go`):
  word-count routing, sentence splitter with UTF-16 offsets, one batch POST per job, strict response validation,
  TechniqueClassification result, built-in display names and explanations, credential redaction.
- Registry: `execution.adapter: technique_classifier` with `short_max_words`, `max_sentences`, `timeout` and the
  `short`/`long` model blocks, validated at load (`internal/registry/technique.go`).
- `adapter.Inputs` adds `Job.input.content` for this adapter only (sentence offsets into the original text).
- `docs/examples/technique_classifier.yaml`, a README section "Technique classifier analyzer".

## Shared specs

Pinned `junction-2026/shared-specs@dea8e5118f85a757eac7f48b985d86183194545f`, change `technique-classifier-result`.
The result object, analyzer id `techniques`, offsets and failure codes are defined there and implemented here
without restating or altering them. The requirements in this change are backend-local: registry settings,
the ML service protocol, routing, the splitter, display texts, redaction and tests.

## Overlap with junction-2026/project#14

`merge-scraper-into-orchestrator` also changes `analyzer-registry`. This change only ADDS requirements there
(no requirement of #14's list is modified), adds a new README section and does not touch the scraper or
bot-detection sections. Whichever PR merges second rebases and keeps both sides.
