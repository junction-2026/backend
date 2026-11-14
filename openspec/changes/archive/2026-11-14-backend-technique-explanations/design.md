## Context

Shared contract: shared-specs @ 73d6f8995f729ad027b1ccb6f79d35e43eed911d, change `technique-explanations-and-llm-techniques`. `techniques` is built in `internal/adapter/technique.go` (scores, detections) and `technique_spans.go` (span step). LLM calls go through `adapter.LLM` (`internal/adapter/llm.go`), which already redacts the API key and header values from provider errors and retries one invalid reply.

## Decisions

1. **Glossary embedded with `go:embed`.** The artifact is `internal/adapter/technique_glossary.yaml` (not under `config/`, because `go:embed` cannot reach outside the package directory and the build stage only copies `cmd` and `internal`). It is parsed and validated when the package initializes, so a missing language, entry or template stops the process at startup and fails every test run. Deployments cannot replace it; a wording change is a code change with a new `version`.
2. **Version pinned by a content hash test.** A test pins the SHA-256 of the artifact next to its `version`; editing any wording without bumping the version (and the pinned hash) fails `go test`.
3. **Templates per language, not per technique.** Each language has one span template (`{name}`, `{span}` once each) and one no-span template (`{name}` once, no `{span}`). Rendering is one `strings.Replacer` pass, so placeholder-like text inside the span is never substituted again.
4. **Language selection.** `explanation_language` is the translation's `source_language` lower-cased when it is one of en, fi, sv, de, fr, es, ru, uk, else `en`. No region subtags are interpreted (the translation returns ISO 639-1 codes).
5. **`llm_techniques` is its own adapter**, not an `llm` task, because it has its own settings (`min_confidence`, `max_findings_per_label`), binds `translation` and reads the job's content and title directly (like `techniques` reads `job_content`). The title is handed to the adapter only for jobs without `scrape`; the adapter then requires it non-empty and different from the content after trimming.
6. **Strict span acceptance.** A finding's `field` must equal the label's field (`title` for `misleading_headline`, `content` otherwise) and its `text` must occur verbatim in the full field string (leftmost occurrence, UTF-16 offsets). Nothing is repaired. Duplicates by (label, field, start, end) keep the highest confidence. The LLM explanation is redacted against configured credentials before it is stored.
7. **Reply shape errors are `invalid_output`.** A reply that is not `{findings: [...]}` with objects of string `label`, `field`, `text`, `explanation` and numeric `confidence` is a schema error (one reply retry via `adapter.LLM`, then `invalid_output`). Unknown labels, out-of-range confidence and unmatched spans only drop the finding.

## Risks / Trade-offs

- English definitions follow the SemEval-2020 Task 11 technique definitions that the NCI technique model card is based on; the model card itself was not reachable from the build environment, so wording may differ slightly and should be reviewed.
- Translations of the glossary were written for this change and should be reviewed by native speakers; a fix needs a new `glossary_version`.
