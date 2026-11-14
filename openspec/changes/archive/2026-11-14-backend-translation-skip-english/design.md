# Design

- The check is a heuristic, deliberately biased toward calling Mistral: it says `english` only when the English
  function-word share is at least `english_min_ratio` and at least twice the best other language's share.
  Short text (fewer than `english_min_words` words) is `unsure`, so tweets, URL-only and emoji-only text go to Mistral.
- The script test (more than 10% non-Latin letters means `not_english`) runs after the word count test.
- The settings are pointers (`*bool`, `*int`, `*float64`) with accessor defaults, like `max_input_chars`, so
  executions built without the registry keep the defaults.
- The `api_key` check stays before the check, so a skipped job and a Mistral job fail the same way on a missing key.
- Known limit: lists are fixed and small; closely related languages with few function words may be `unsure`,
  which only costs a Mistral call.
