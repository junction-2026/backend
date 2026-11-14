# Proposal: LLM topic and news extraction analyzer (topic_extraction)

## Why

Parent feature junction-2026/project#9, child junction-2026/backend#26. A later fact-check step needs to know what to
check in a submitted text. A regular analyzer `topic_extraction` asks an OpenAI-compatible LLM for the fact-checkable
topics (with a search query) and the news items the text refers to. Until now the `llm` adapter ran only as the finalizer.

## What Changes

- `adapter: llm` is also allowed on regular analyzers with `execution.task: topic_extraction`; new setting `task`
  and, for that task, `max_input_chars`; default prompt `config/extractor/prompt.md` (copied into the image).
- `internal/adapter/topic.go`: one chat/completions call through the existing `llm.go` client, input cut to
  `max_input_chars`, reply validated and normalized into the shared TopicExtraction object.
- `docs/examples/topic_extraction.yaml` (variables `EXTRACTOR_BASE_URL`, `EXTRACTOR_API_KEY`, `EXTRACTOR_MODEL`),
  README section "Topic extraction analyzer", mock-LLM tests.
- The finalizer is unchanged.

## Shared specs

Pinned `junction-2026/shared-specs@fb681f718322edadf98279abc9ea08079e3dbde3`, change `llm-topic-extraction`
(capability `topic-extraction-result`): result object, input rules, output validation and normalization, failures and
credential-free errors. This change implements those requirements and adds only backend-local ones (registry
configuration, request shape, prompt, example).

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `analyzer-registry`: LLM execution settings and validation, LLM startup checks; new Topic extraction prompt default and example
- `analyzer-adapters`: new Topic extraction request, input limit, validation and normalization, failures

## Impact

`internal/registry/registry.go`, `internal/adapter/{adapter,llm,topic}.go`, tests, `config/extractor/prompt.md`,
`Dockerfile` (one COPY line), `docs/examples/topic_extraction.yaml`, `README.md`.
