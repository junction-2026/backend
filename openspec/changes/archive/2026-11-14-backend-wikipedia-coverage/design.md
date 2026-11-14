# Design: backend-wikipedia-coverage

## Decisions
1. **Placement.** The label is a pure function `wikiCoverage(claim, wikiLookup)` in a new file, called from
   `personalize` after `matched_entities`/`matched_numbers`. `personalize` runs for fresh answers, cache hits and
   in-run duplicates, so the label always reflects the current claim and cached/uncached answers agree. Keeping the
   code out of `wikipedia.go` keeps the merge with #47 (ClaimLookup level) mechanical.
2. **Rules** follow the shared "Wikipedia coverage rule order" literally: named matched entities, digit-run claim
   numbers (4 digits = year), first aligned sentence, then first conflicting sentence with a standalone number.
   No word-number or date-format normalization; a date written `2025-05-03` against "3 May 2025" therefore can give
   `conflicts` on `05`, which the no-verdict rule and the finalizer prompt cover.
3. **Downgrade.** `verified` re-checks the chosen sentence (non-empty substring of `passage`, holding the needed
   numbers) before `aligned`/`conflicts` is emitted; on failure the label is `mentioned`.
4. **Quote verification** runs after the finalizer output passed normalization and validation, on the decoded
   output, so a bad quote never makes the output invalid or triggers an LLM reply retry. The wikipedia run is found
   by its registry adapter (`wikipedia`), not by id. A covered lookup whose `aligned`/`conflicts` evidence is
   unverifiable is treated (and copied) as `mentioned`. Dropped quotes are logged at debug level with job id and a
   reason, never the quote text.
5. **Strict schema.** OpenAI strict `json_schema` mode requires every property in `required`, so
   `wikipedia_quotes` is required and the prompt says to send `[]` when nothing is quoted.
