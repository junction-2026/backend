## Decisions

- One function, `adapter.JobSourceLanguage(job)`, serves both the `$job.source_language` binding and the llm user message, so the two always agree. It reuses `readSourceLanguage` (trim, lower-case, `^[a-z]{2}$`) on the result of the succeeded `translation` run and returns "" otherwise.
- The binding member is always present: `nil` (JSON null) when unknown.
- Only the validated two-letter code is interpolated into the prompt, so the element cannot carry injected text.
- When the language is unknown the user message is byte-identical to the previous format; the system message stays the prompt file.
- A custom `prompt_file` that does not mention `<source_language>` keeps the previous behaviour; documented in the example.
