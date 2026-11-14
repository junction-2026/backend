# backend

Analysis orchestrator HTTP server (`cmd/orchestrator`).

| Variable | Default | |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | listen address |
| `ANALYZERS_CONFIG` | `/etc/orchestrator/analyzers` | registry file or directory |
| `MAX_PARALLEL_ANALYZERS`, `MAX_PARALLEL_JOBS`, `JOB_RETENTION` | 8, 16, 24h | engine limits |

Optional analyzers: `docs/examples/propaganda_detection.yaml` defines two analyzers. `translation`
(adapter `mistral_translation`) translates the job's `normalized_content` into English through the Mistral
chat-completions API, and `propaganda_detection` runs the
[IDA-SERICS/PropagandaDetection](https://github.com/IDA-SERICS/PropagandaDetection) service on that
translation (`depends_on: [translation]`, `input.bindings: {text: $analyzers.translation}`); the service never
sees the original text. If the translation fails, propaganda detection is skipped (`dependency_failed`).
Copy the file into the directory `ANALYZERS_CONFIG` points to and set:

| Variable | |
|---|---|
| `MISTRAL_API_KEY` | Mistral API key (`execution.api_key`, required) |
| `PROPAGANDA_DETECTION_URL` | the service's endpoint, e.g. `http://propaganda-detection:8000/predict` |

An enabled analyzer with an unset variable aborts startup. The example is not part of `config/analyzers`.
Optional `translation` settings: `model` (default `mistral-small-latest`, `${VAR}` allowed), `timeout`
(default `60s`), `endpoint` (default `https://api.mistral.ai/v1/chat/completions`), `headers` and
`max_input_chars` (default `20000`). Longer input is **truncated** at the last whitespace before the limit
(no chunking); the result then has `truncated: true` and `input_characters` holds the original length.
Cost and latency: every job runs one extra paid LLM call (up to 60s) before propaganda detection starts,
so `translation` adds that delay and Mistral token cost to each analysis. To analyze the original text
instead, remove `translation`, `depends_on` and `input` from the example.

```
go run ./cmd/orchestrator            # with ANALYZERS_CONFIG=config/analyzers for the sample registry
docker build -t orchestrator . && docker run -p 8080:8080 orchestrator
```

`GET /healthz` is an operational endpoint; the API is under `/api/v1`. There is no authentication:
keep the server on a trusted network.
