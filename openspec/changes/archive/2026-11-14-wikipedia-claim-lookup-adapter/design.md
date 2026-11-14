# Design: `wikipedia` analyzer (backend)

Shared contract: shared-specs @ 686777ee4bc9db7b279b51d3c00a219ff02d39a1, change `wikipedia-claim-lookup`.

## Decisions
1. **Two requests per lookup.** `list=search` (srlimit=max_hits, snippet) then one `prop=pageprops|revisions|extracts`
   request over the titles of all hits (`redirects=1`, `exintro`, `explaintext`), so disambiguation, entity-title
   selection and the revision are decided from one answer. No hits: one request. `max_hits` is capped at 20, the
   MediaWiki limit of intro extracts per request.
2. **Selection** uses the search hit title for entity matching and `pageprops.disambiguation` of the resolved page
   for disambiguation. Hits whose page is missing in the second answer are dropped.
3. **Passage.** title mode: plain-text lead (cut to `max_passage_chars` runes); fulltext mode: the snippet with tags
   removed and `html.UnescapeString`. When the preferred text is empty the other one is used (`passage_source` says
   which); when both are empty the lookup is `no_page` (no passage is invented).
4. **Cache key** is sha256(language, normalised query, search_mode) as the issue specifies; entities are not in the key,
   so `matched_entities`/`matched_numbers` are recomputed from the stored passage for the current claim on every
   answer. The page chosen for a title-mode lookup may therefore come from an earlier claim with the same query
   (accepted: same query, same wiki).
5. **Run-level reuse.** All lookups of a run register a flight per key in claim order before any request starts, so
   the first identical lookup asks and the others wait for it (counted as cached); the order is deterministic.
6. **Failure is all-or-nothing.** The first non-recoverable request error cancels the others; the run returns
   `adapter_error` naming the wiki (`{lang}.wikipedia.org`) and the HTTP status, never a URL. Context deadline and
   cancellation map to `timeout` and `cancelled` and win over a failure they caused.
7. **Cache writes are best effort** (temp file + rename in the cache directory); an unwritable directory costs
   requests, not the run.
8. **User-Agent** is `JunctionOrchestrator/1.0 (<execution.user_agent>)`; the contact is mandatory and `${VAR}`-expandable.
