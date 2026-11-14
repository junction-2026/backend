# backend

Analysis orchestrator HTTP server (`cmd/orchestrator`).

| Variable | Default | |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | listen address |
| `ANALYZERS_CONFIG` | `/etc/orchestrator/analyzers` | registry file or directory |
| `MAX_PARALLEL_ANALYZERS`, `MAX_PARALLEL_JOBS`, `JOB_RETENTION` | 8, 16, 24h | engine limits |

Optional analyzer: `docs/examples/propaganda_detection.yaml` runs the
[IDA-SERICS/PropagandaDetection](https://github.com/IDA-SERICS/PropagandaDetection) service
(adapter `propaganda_detection`). Copy it into the directory `ANALYZERS_CONFIG` points to and set
`PROPAGANDA_DETECTION_URL` to the service's endpoint (an enabled analyzer with an unset variable
aborts startup). It is not part of `config/analyzers`.

Optional LLM finalizer: `docs/examples/final_synthesis.yaml` (adapter `llm`) sends a verdict
request to any OpenAI-compatible `POST {base_url}/chat/completions` endpoint once all analyzers
settled. Copy it next to your analyzers and set:

| Variable | |
|---|---|
| `FINALIZER_BASE_URL` | API base URL, e.g. `https://api.openai.com/v1` (`/chat/completions` is appended) |
| `FINALIZER_API_KEY` | sent as `Authorization: Bearer <key>`; never logged, returned or put in error messages |
| `FINALIZER_MODEL` | model name, e.g. `gpt-4o-mini` (no default) |

An enabled `llm` finalizer with an empty `base_url`, `api_key` or `model`, or an unreadable
`prompt_file` (default `/etc/orchestrator/finalizer/prompt.md`, shipped in the image from
`config/finalizer/prompt.md`), aborts startup. `response_format` selects how the reply schema is
enforced: `json_schema` (default, strict structured output), `json_object` (JSON mode, the schema
goes in the user message) or `none` (no `response_format` member, for servers without JSON mode;
the schema goes in the user message). Every reply is validated; an invalid one fails only the
finalization (`invalid_output`). Only analyzers with `llm: {include: true, description: ...}` reach
the LLM (see `propaganda_detection.yaml`); with none opted in, every job ends `uncertain`. Optional
`headers` and `extra_body` are sent as configured.

```
go run ./cmd/orchestrator            # with ANALYZERS_CONFIG=config/analyzers for the sample registry
docker build -t orchestrator . && docker run -p 8080:8080 orchestrator
```

`GET /healthz` is an operational endpoint; the API is under `/api/v1`. There is no authentication:
keep the server on a trusted network.
