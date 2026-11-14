# Design: backend-technique-all-labels

## Routing

`words = len(strings.Fields(translated_text))`. `nci-technique` runs when `words >= technique_min_words` or
`words > social_max_words`; `nci-social` when `words <= social_max_words`. Results are ordered `nci-technique`, then
`nci-social`. The load check `technique_min_words <= social_max_words + 1` guarantees at least one model.

## Calls

Both selected models are called in goroutines under the analyzer context. The first failure cancels the other call and
becomes the run error (all or nothing, shared requirement "No partial technique result"), so a failing model does not
wait for a slow one. Requests carry only `translated_text`; `job_content` is read only by the span step.

## Response parsing

One parser serves the classification models and the span step's sentence model: one list per text sent, every known
label exactly once, non-empty labels, no duplicates, scores in [0, 1]. Unfamiliar labels are kept in model order. The
sentence model (also `nci-social`) therefore tolerates unfamiliar labels too; the span step ignores them because it only
looks up detected (known) names.

## Old keys

The YAML decoder rejects unknown keys. To name the replacement, the old keys are decoded into `any` fields and rejected
in `checkTechnique` with `execution.<old> is no longer supported: use execution.<new>`. `threshold`/`thresholds` are
rejected inside the `technique` and `social` blocks; inside `spans.sentence_model` they stay valid.

## Cutoffs

`NCIModel.Cutoff` is optional; `CutoffOr` applies the defaults 0.5 / 0.3 so tests can build Executions without Load.
`above_cutoff = score >= cutoff`.
