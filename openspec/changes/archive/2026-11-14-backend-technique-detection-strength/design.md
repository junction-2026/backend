# Design

- `strong_margin` is decoded as `any` so a non-number (string, bool, list) is reported by `checkTechnique` with the
  file and analyzer id instead of a bare YAML decode error. `StrongMarginN()` returns the value or 0.15.
- `strengthOf` runs after the span step and explanation: margin = max(`score - cutoff`) over the ModelResults whose
  `model` is in the Detection's `models`, rounded with `math.Round(m*100)/100` (half away from zero). `strength`
  compares the rounded margin, so 0.65 - 0.5 is `strong` at 0.15.
- The finalizer runtime is unchanged: the new members reach the LLM through the existing techniques result entry.
- The prompt keeps the availability rules; `not_applicable` analyzers are named as never a reason for `uncertain`
  and are not listed as hints that lower confidence (the availability rule says they never lower it).
