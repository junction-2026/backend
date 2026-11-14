# Tasks

## 1. Registry
- [x] 1.1 `technique` / `social` blocks with `cutoff`, `social_max_words`, `technique_min_words` and their relation
- [x] 1.2 Reject `short`, `long`, `threshold`, `thresholds`, `short_max_words`, `max_sentences` naming the replacement
- [x] 1.3 `spans` block unchanged and still validated

## 2. Adapter
- [x] 2.1 Routing and one concurrent `POST {"text": [translated_text]}` per selected model; sentence batches removed
- [x] 2.2 Response validation, unfamiliar labels kept, `results` ordering
- [x] 2.3 `detected` from `above_cutoff` with `models`, fed to the span step; removed members gone
- [x] 2.4 Credentials of both models redacted from errors

## 3. Docs and tests
- [x] 3.1 Example YAML with the new keys and `llm.description`; README section
- [x] 3.2 Stub tests: routing 39/40/80/81, cutoff equality and 0.49, unfamiliar label, no averaging, partial failure,
  non-English job, old keys and bound relation; span and propaganda tests green
