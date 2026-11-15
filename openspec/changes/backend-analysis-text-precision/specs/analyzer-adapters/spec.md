## MODIFIED Requirements

### Requirement: Extractor prompt query wording
`config/extractor/prompt.md` SHALL require `query` and `query_en` of 3-8 words naming the actor, the assertion and the key number or place, and SHALL list what to leave out: dates and years, relative time, numbers unless the number is the claim, verbs of deciding or saying, hedges and framing. It SHALL give "Taiwan country" as a bad query with a good counterpart. `text` SHALL stay the full claim.

#### Scenario: Prompt content
- **WHEN** the prompt file is read
- **THEN** it states the 3-8 word rule, the leave-out list and the "Taiwan country" bad / good query example

## ADDED Requirements

### Requirement: Extractor prompt claim splitting
`config/extractor/prompt.md` SHALL require one checkable assertion per claim, with the example "They're given housing preference, organise Land Grabs, bring violent crime and vote ANC." split into four claims (housing preference, organise land grabs, bring violent crime, vote ANC); it SHALL keep `max_claims` at 5 by dropping the least check-worthy claims and SHALL rate generalisations about the behaviour of a group of people at 0.6 or more.

#### Scenario: Prompt content
- **WHEN** the prompt file is read
- **THEN** it contains the one-assertion rule, the four-claim example and the group-generalisation rule with 0.6

### Requirement: LLM techniques prompt certainty definition
`config/llm_techniques/prompt.md` SHALL define `evidence_certainty_mismatch` as a contested, causal or sweeping claim presented as certain without evidence, say that reporting events, figures or statements attributed to a named source and the ordinary declarative style of news reports are not a mismatch, and give one attributed news example (not flagged) and one unsupported-certainty example (flagged).

#### Scenario: Prompt content
- **WHEN** the prompt file is read
- **THEN** it contains the definition, "The ministry said 40 people were killed in the strike on Tuesday." as not flagged and "This definitely proves the vaccine was designed to harm us." as flagged

### Requirement: Finalizer-only language check option
`adapter.LLM` and `llmChat` SHALL keep their signatures and behaviour for every caller other than the finalizer; the language check SHALL be an extra option used only by the finalizer, so topic_extraction, term_context and llm_techniques keep their retry behaviour.

#### Scenario: Other callers unchanged
- **WHEN** topic_extraction, term_context or llm_techniques call the LLM
- **THEN** their requests, retries and results are as before this change
