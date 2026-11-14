## Why

The finalizer is told to write `user_text` "in the language of the content" but gets no explicit language signal, so it may answer in English for a Russian article. The `translation` analyzer already yields `source_language`. Parent: junction-2026/project#63.

## What Changes

- The `llm` finalizer adds `<source_language>{code}</source_language>` to the user message when the job's source language is known; the default `config/finalizer/prompt.md` tells the model to write `user_text` in that language.
- `http_sync`/`http_async` finalizers may bind the new shared source `$job.source_language` (shared change `finalizer-source-language`, analyzer-service-protocol @ 9c235a5928bc1af3d777f5b1f9bcdfea0ad12657); the registry accepts it only on `after_all_analyzers_settled` analyzers.
- `docs/examples/final_synthesis.yaml` documents the binding and the custom-prompt note.

Non-goals: verdict logic, FinalAssessment shape, frontend, post-hoc translation.

## Impact

Code: `internal/adapter/adapter.go`, `internal/adapter/llm.go`, `internal/registry/registry.go`, `internal/finalizer/finalizer.go`, `config/finalizer/prompt.md`, `docs/examples/final_synthesis.yaml`. Existing configurations and unbound HTTP finalizers behave as before.
