# Design: term_context (backend)

- **Wikipedia reuse.** `term_context` builds a `wikiRun` (HTTP client, `User-Agent`, concurrency semaphore, single
  retry of 429/5xx, request counter) and the on-disk `wikiCache` of the `wikipedia` adapter. Term entries use the
  search mode `term` in the cache key, so they never collide with claim lookups. A `found` entry stores the first lead
  sentence in `passage`; `no_page` stands for every `no_coverage` outcome (no page, disambiguation, empty lead).
- **Two requests per term.** Request 1 resolves the title with `redirects=1` and returns lead, page props and revision.
  Only when the title has no page, request 2 is `generator=search` with the same `prop`s, so the lead of every hit
  comes back in one answer; the first hit whose title equals the term (case-insensitively, optionally followed by a
  parenthesised qualifier) is the own page.
- **Prompts live in code** (`term_context_prompt.go`), not in a prompt file, because they enforce the "no
  definitions, no camps, no verdicts" contract; operators cannot loosen them by editing a file.
- **Original text.** `Inputs` passes `Job.input.content`. When the `topic_extraction` result has
  `input_truncated: true` the text is cut at a space to `max_input_chars` (default as `topic_extraction`'s).
- **Sentence boundaries** are computed on runes; `in_post` is a substring of the text, so it needs no LLM.
- **`sense`.** `unknown` by rule when the term has no other quote; otherwise one comparison call for all such terms
  over `term`, `in_post`, `quote` and `snippet`s only. Its reply is validated before use; QuoteRefs are the only model
  output that reaches the result besides the `same`/`differs` enum.
