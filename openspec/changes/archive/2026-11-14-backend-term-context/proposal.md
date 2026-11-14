# Proposal: term_context analyzer (backend)

## Why

Parent feature junction-2026/project#49, child issue junction-2026/backend#134. Implements the shared change
`term-context` (shared-specs @ 5bd40738633bfa21b0e371a364003408af42e773, capability `term-context-result`): a new
selectable analyzer `term_context` that quotes, per loaded or unfamiliar term of a post, the post's own sentence, the
verbatim lead sentence of the term's own Wikipedia page and verbatim `news_search` snippets, and says whether they use
the term for the same thing. The result contract is shared; prompts, matching code, cache use, registry keys and
progress facts are backend-local (common rule 13).

## What Changes

- `internal/registry/term_context.go` (new) and `registry.go`: adapter `term_context`, keys `max_terms`,
  `in_post_max_chars`, `quote_max_chars`, `elsewhere_max` (defaults 5, 300, 400, 3), LLM keys without `prompt_file`,
  Wikipedia transport keys shared with the `wikipedia` adapter.
- `internal/adapter/term_context*.go` (new): selection and comparison prompts, verbatim term matching, sentence
  extraction, own-page lookup with the existing Wikipedia client and cache, snippet matching, `sense`.
- `internal/adapter/llm.go`: `LLM` gains a variant that takes the system prompt and reply schema as arguments.
- `internal/adapter/activity_facts.go`, `internal/engine/activity.go`: activity text and fact for `term_context`.
- `docs/examples/term_context.yaml` (new), README section.

Not changed: shared specs, frontend, the `wikipedia` analyzer's result, `news_search` (no extra search is sent).

## Impact

backend only. At most two LLM calls and two Wikipedia requests per kept term per job; no new HTTP endpoint.
Deployments without the registry entry are unaffected.
