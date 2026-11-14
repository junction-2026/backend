## REMOVED Requirements

### Requirement: Uncertain rule
**Reason**: Weak evidence must no longer lead to `uncertain` (junction-2026/project#31).
**Migration**: Replaced by the Strong signal verdict rules requirement.

## ADDED Requirements

### Requirement: Strong signals in the finalizer prompt
`config/finalizer/prompt.md` SHALL define a strong signal as any of: a `techniques` detection with `strength` `strong`; an `llm_techniques` finding with `confidence` >= 0.8; a fact-check whose rating contradicts a claim in the text; news coverage that clearly contradicts a claim; `propaganda_detection` = PROPAGANDA.

#### Scenario: Strong-signal list
- **WHEN** the LLM finalizer calls a stub LLM
- **THEN** the system message contains the five kinds of strong signal

### Requirement: Strong signal verdict rules
The prompt's verdict rules SHALL be: `detected` = at least one strong signal the content supports; `not_detected` = no strong signal (weak technique detections and low-confidence findings only lower confidence; they and `not_applicable` analyzers are never a reason for `uncertain`); `uncertain` = strong signals disagree, or a decisive applicable analyzer is unavailable. The old weak-evidence `uncertain` rule and "Prefer uncertain" sentence SHALL be gone.

#### Scenario: New verdict rules
- **WHEN** the LLM finalizer calls a stub LLM
- **THEN** the system message contains the three rules and not "evidence is weak, mixed or contradictory"

#### Scenario: Only weak detections
- **WHEN** a job's only signals are weak technique detections and `propaganda_detection` NO_PROPAGANDA
- **THEN** the finalizer's user message lists every Detection with `strength: "weak"`, `margin` and `located`, and the techniques description

### Requirement: Finalizer confidence guidance
The prompt SHALL say: `not_detected` with no weak hints has confidence 0.8 or more, with some weak hints 0.6 to 0.8; `detected` confidence scales with the number and agreement of strong signals.

#### Scenario: Confidence guidance
- **WHEN** the system message is read
- **THEN** it contains both confidence ranges for `not_detected` and the `detected` scaling rule

### Requirement: Finalizer consistency rule
The prompt SHALL require `user_text` to agree with the verdict (for example no "this looks normal" under `uncertain`). The insufficient-evidence short-circuit and the availability rules SHALL stay unchanged.

#### Scenario: Consistency rule
- **WHEN** the system message is read
- **THEN** it says `user_text` must agree with the verdict and still contains the `not_applicable` availability rule
