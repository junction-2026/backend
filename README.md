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

## Topic extraction analyzer

`docs/examples/topic_extraction.yaml` (analyzer `topic_extraction`, adapter `llm`, `execution.task:
topic_extraction`) asks any OpenAI-compatible `POST {base_url}/chat/completions` endpoint for the
fact-checkable **topics** (`title`, `query` search phrase, `description`) and **news** items (`headline`,
`summary`) in the job's `normalized_content`. The result is `{topics, news, input_truncated, usage}`; empty
lists are a success. Copy the file next to your analyzers and set (independent of the finalizer's variables):

| Variable | |
|---|---|
| `EXTRACTOR_BASE_URL` | API base URL, e.g. `https://api.openai.com/v1` (`/chat/completions` is appended) |
| `EXTRACTOR_API_KEY` | sent as `Authorization: Bearer <key>`; never logged, returned or put in error messages |
| `EXTRACTOR_MODEL` | model name, e.g. `gpt-4o-mini` (no default) |

Settings: `base_url`, `api_key`, `model` (required), `prompt_file` (default
`/etc/orchestrator/extractor/prompt.md`, shipped in the image from `config/extractor/prompt.md`; it asks for at
most 10 topics and 10 news items in the input's language and treats the text as data), `max_input_chars`
(default `20000`; longer input is cut at the last whitespace before the limit and the result has
`input_truncated: true`), `temperature` (default 0), `max_tokens` (default 2000), `timeout` (default `30s`),
`response_format` (`json_schema` default, `json_object` or `none`; the schema goes in the user message in the
last two), `headers` and `extra_body` (not `model`/`messages`); all string values accept `${VAR}`. An enabled
analyzer with an empty `base_url`, `api_key` or `model`, or an unreadable `prompt_file`, aborts startup. Blank
input fails with `adapter_error` without an LLM call; a reply that is not the expected shape fails only this
analyzer (`invalid_output`). One 429/5xx answer is retried once. Add `llm: {include: true, description: ...}` to
let the LLM finalizer see the extraction. Every job that runs it makes one paid LLM call.

```
go run ./cmd/orchestrator            # with ANALYZERS_CONFIG=config/analyzers for the sample registry
docker build -t orchestrator . && docker run -p 8080:8080 orchestrator
```

`GET /healthz` is an operational endpoint; the API is under `/api/v1`. There is no authentication:
keep the server on a trusted network.
