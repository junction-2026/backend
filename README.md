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

## News coverage analyzer

`docs/examples/news_search.yaml` (analyzer `news_search`, adapter `news_search`) searches news and web coverage of
the topics and news items that `topic_extraction` found, through [Serper](https://serper.dev), in the text's
language and in English, so the finalizer and analysts can see whether and how outlets report the story (result
shape: shared change `news-search-result`). It depends on `topic_extraction` (binding `topics`) and, for the
`source` language, on a `translation` analyzer (binding `translation`, optional). Set (independent of the other
analyzers' variables):

| Variable | |
|---|---|
| `SERPER_BASE_URL` | e.g. `https://google.serper.dev` (`/news` and `/search` are appended) |
| `SERPER_API_KEY` | sent as `X-API-KEY`; never logged, returned or put in error messages |
| `MISTRAL_API_KEY` | `translate.api_key`, sent as `Authorization: Bearer <key>` to translate the queries; never logged or returned |

Settings (defaults): `languages` (`[source, en]`; `source` is the translation's `source_language`, dropped when
unknown, else ISO 639-1 codes), `translate.model` (`mistral-small-latest`), `translate.endpoint` (Mistral chat
completions), `max_topics` (3) and `max_news` (2) searched items, `num` (8) results per search, `min_results` (2),
`per_domain` (2) and `max_results` (12) articles, `max_concurrency` (4) provider requests in flight per job,
`cache_ttl` (`6h`), `timeout` (`20s`). Non-positive values, unknown language tokens and missing keys abort startup
with a message naming the key; `translate.api_key` is required when a language other than `source` is configured.

How it works: the first `max_topics` topic `query` strings, then the first `max_news` headlines, are searched once
per language; queries not in the target language (everything, when the source language is unknown) are translated
with one Mistral request per target language. Each query and language is one `POST {base_url}/news`
(`gl` from the language: en→us, de→de, fr→fr, else the code); when it returns fewer than `min_results` results,
one `POST {base_url}/search` follows for that query and language only. 429 and 5xx answers are retried once after
1s. Successful answers are cached in memory per process for `cache_ttl` (identical concurrent requests share one),
so repeating a job costs no searches. A 401/403 from Serper or Mistral fails the analyzer at once
(`adapter_error`, naming `execution.api_key` or `execution.translate.api_key`); when some queries fail the analyzer
still succeeds and lists them (`search_failed`, `translation_failed`); when all fail it fails. Nothing to search
settles the run as `skipped` with reason `no_searchable_topics`, without any request.

**Quota cost** per job: at most (topics + news) x languages x 2 Serper searches (news plus one fallback), at most
(3 + 2) x 2 x 2 = 20 with the defaults, plus one retry per 429/5xx answer, minus cached searches; the result reports
`searches_used` and `cached`. Translation costs at most one Mistral call per non-source language.

```
go run ./cmd/orchestrator            # with ANALYZERS_CONFIG=config/analyzers for the sample registry
docker build -t orchestrator . && docker run -p 8080:8080 orchestrator
```

`GET /healthz` is an operational endpoint; the API is under `/api/v1`. There is no authentication:
keep the server on a trusted network.

## Jobs from a social-media URL

`POST /api/v1/jobs` also accepts `{"scrape": {"url": ..., "max_posts": 20, "max_comments": 50}}` instead of
`input` (shared change `social-media-scrape-input`). The job answers 201 at once with `input: null` and status
`queued` or `scraping`; the orchestrator then calls the scraper service (`POST {SCRAPER_URL}/scrape`), maps the
post text (post URL) or the posting history, newest first (profile URL), to `input.content` and keeps the full
scrape result in `input.source.metadata.scrape`. A failed scrape ends the job `failed` with `scrape.error.code`
(for example `account_private`, `unsupported_url`, `scraper_unavailable`, `timeout`, `no_text_content`) and
skips analyzers and finalization with `input_unavailable`.

| Variable | Default | |
|---|---|---|
| `SCRAPER_URL` | unset | scraper base URL, e.g. `http://scraper:8003`; unset: requests with `scrape` answer 503 `scrape_unavailable` |
| `SCRAPER_TOKEN` | | sent as `Authorization: Bearer <token>`; required when `SCRAPER_URL` is set |
| `SCRAPER_TIMEOUT` | `120s` | Go duration; a slower scrape fails with `timeout` |

None of the three values is logged or returned by the API.

```
curl -s -X POST http://localhost:8080/api/v1/jobs -H 'Content-Type: application/json' \
  -d '{"scrape": {"url": "https://bsky.app/profile/alice.bsky.social/post/3kxyz", "max_posts": 10}}'
```

Follow the job with `GET /api/v1/jobs/{id}` or the event stream (`scrape.started`, then `scrape.succeeded` or
`scrape.failed`). `docs/examples/bot_detection.yaml` is a disabled example analyzer for the bot detector
service (port 8001): it binds `$input` and `$input.source`, so it receives the profile, history and comments.
Set `BOT_DETECTION_URL` (e.g. `http://bot-detector:8001/predict`), set `enabled: true` once the service accepts
the standard analyzer request, and copy the file into the directory `ANALYZERS_CONFIG` points to.
