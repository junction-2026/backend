# Design: technique_classifier adapter

- **Registry types** live in `internal/registry/technique.go` (`TechniqueModel`, `TechniqueNames`) so that
  `registry.go` only gets small hooks and merges cleanly with #14.
- **Original text for offsets**: bindings are restricted to `text`, so `adapter.Inputs` adds the member
  `job_content` (`Job.input.content`) for this adapter only. It is not part of any registry binding.
- **Sentence index**: the response lists the full text first. When the single sentence equals the (trimmed) full
  text only one item is sent and its scores serve both.
- **Redaction**: all configured header values of both models (and a bearer token without its scheme) are removed
  from messages; messages also never repeat response content. Non-2xx answers report only the status code.
- **Splitter shortcut**: no abbreviation or decimal handling; sentences are cut at every `.`, `!`, `?`.
