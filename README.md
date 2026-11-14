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
`summary`) and checkable **claims** in the job's `normalized_content`. The result is `{topics, news, claims, input_truncated, usage}`; empty
lists are a success. Copy the file next to your analyzers and set (independent of the finalizer's variables):

| Variable | |
|---|---|
| `EXTRACTOR_BASE_URL` | API base URL, e.g. `https://api.openai.com/v1` (`/chat/completions` is appended) |
| `EXTRACTOR_API_KEY` | sent as `Authorization: Bearer <key>`; never logged, returned or put in error messages |
| `EXTRACTOR_MODEL` | model name, e.g. `gpt-4o-mini` (no default) |

Settings: `base_url`, `api_key`, `model` (required), `prompt_file` (default
`/etc/orchestrator/extractor/prompt.md`, shipped in the image from `config/extractor/prompt.md`; it asks for at
most 10 topics, 10 news items and 5 claims in the input's language and treats the text as data), `max_input_chars`
(default `20000`; longer input is cut at the last whitespace before the limit and the result has
`input_truncated: true`), `temperature` (default 0), `max_tokens` (default 2000), `timeout` (default `30s`),
`response_format` (`json_schema` default, `json_object` or `none`; the schema goes in the user message in the
last two), `headers` and `extra_body` (not `model`/`messages`); all string values accept `${VAR}`. An enabled
analyzer with an empty `base_url`, `api_key` or `model`, or an unreadable `prompt_file`, aborts startup. Blank
input fails with `adapter_error` without an LLM call; a reply that is not the expected shape fails only this
analyzer (`invalid_output`). One 429/5xx answer is retried once. Add `llm: {include: true, description: ...}` to
let the LLM finalizer see the extraction. Every job that runs it makes one paid LLM call.

Claims: the reply also holds `claims` (`text`, `language`, `query` of 3-8 words, `query_en`, `entities`,
`check_worthiness` 0..1): checkable statements of fact only (no opinions, predictions, questions), ordered by
`check_worthiness` and capped by `max_claims` (default 5). A reply without `claims` fails with `invalid_output`;
`claims: []` is a success.

## Fact check analyzer

`docs/examples/fact_check.yaml` (analyzer `fact_check`, adapter `fact_check`, `depends_on: [topic_extraction]`)
searches the claims of the extraction in [Google Fact Check Tools](https://developers.google.com/fact-check/tools/api)
(`GET {base_url}/claims:search`) and returns `{queries, fact_checks, requests_used, cached}`. No LLM is called.

| Variable | |
|---|---|
| `GOOGLE_FACTCHECK_API_KEY` | API key, sent only as the `key=` query parameter; never logged, returned or put in errors. The **Fact Check Tools API must be enabled** on the key's Google Cloud project, otherwise the analyzer fails with `adapter_error` (HTTP 403) |

Claims with `check_worthiness` of at least `min_check_worthiness` (default 0.4) are searched, at most
`max_claims` (default 3): the claim's `query` in its language and, unless it is English, `query_en` in English
(at most 2 requests per claim; equal pairs are sent once), `max_concurrency` (default 4) at a time. Other
settings: `page_size` (5), `max_age_days` (1825), `max_results` (10, newest review first), `cache_ttl` (24h,
in memory only, successful answers per query), `timeout` (15s), `base_url`. A 429 or 5xx is retried once; a query
that still fails is reported in `queries` and the analyzer succeeds unless every query failed. HTTP 400/401/403
fails the analyzer at once. Without a qualifying claim (or an extraction without `claims`) the analyzer is
`skipped` with reason `no_checkable_claims` and sends no request. `rating_normalized` (`false`, `misleading`,
`mixed`, `true`, `unrated`) comes from a built-in term table; `rating` is the provider's text. A match is a
similar checked claim: compare `claim` with `for_claim`.

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

## Source info analyzer

`docs/examples/source_info.yaml` (analyzer `source_info`, adapter `source_info`) looks up the news outlet, its
political leaning (`bias`), factuality and country behind every article `news_search` found and behind the job's
`input.source.url` (`page_outlet`), and summarises the leaning and country mix (result shape: shared change
`source-info-result`). It depends on `news_search` (binding `search`, required) and may bind `source: $input.source`.
It makes no network requests and costs no quota; leaning and factuality describe the outlet, not the article, and
`unknown` means not rated.

`execution.outlets_file` (default `/etc/orchestrator/data/outlets.csv`, where the image copies `data/outlets.csv`;
format in `data/README.md`) is read once at startup when the analyzer is enabled. Startup fails naming the analyzer
and file when it is missing, when the header differs, when a `domain` is empty or when a host or domain is listed
under two outlets (the row and value are named). `outlets_file` and `timeout` are the only `execution` keys.
Analyzers with the same file share one in-memory table.

A URL is matched by its exact host (lower case, no port, no trailing dot, no leading `www.`), then by its registered
domain (public suffix list). Nothing found by `news_search` and no `input.source.url` settles the run as `skipped`
with reason `no_sources`; a failed or skipped `news_search` gives the usual `dependency_failed` /
`dependency_skipped`.

## Technique classifier analyzer

`docs/examples/technique_classifier.yaml` (analyzer `techniques`, adapter `technique_classifier`, category `propaganda`,
`depends_on: [translation]`, `input.bindings: {text: $analyzers.translation}`) scores the English translation for the 18
SemEval-2020 Task 11 propaganda techniques and finds the sentences that carry them (result shape: shared change
`technique-classifier-result`). Add the file next to an analyzer `translation` (see `propaganda_detection.yaml`) and set:

| Variable | |
|---|---|
| `TECHNIQUE_SHORT_URL` | prediction endpoint of the short-text model (BERTweet) |
| `TECHNIQUE_LONG_URL` | prediction endpoint of the long-text model (ModernBERT) |
| `ML_API_TOKEN` | sent as `Authorization: Bearer <token>` to both; never logged, returned or put in error messages |

The model is chosen by the word count of `translated_text` (whitespace-separated): up to `short_max_words` (default 80)
the `short` model, otherwise the `long` model; `input.source` is ignored. The text is split at `.`, `!`, `?` and
newlines (fragments under 3 words are dropped) and one `POST {"text": [full text, sentence 1, ..., sentence N]}` goes
to the chosen service, N at most `max_sentences` (default 20; `sentences_truncated` is true when more were left
unscored). The answer must be `{"result": [[{label, score} x 18], ...], "latency_ms"?}`, one list per text sent with
exactly the 18 technique names and scores in 0..1, else the analyzer fails with `invalid_output`. There is no retry and
no fallback to the other model (HTTP errors are `adapter_error`, a slow answer `timeout`; default `timeout: 30s`).

Each of the `short` and `long` blocks takes `endpoint` (required), `headers`, `threshold` (required, 0..1) and an
optional `thresholds` map of per-technique overrides (technique name: 0..1); string values accept `${VAR}`. The
example uses 0.3 for the short and 0.5 for the long model. An enabled analyzer with an unset variable, a missing block,
an empty endpoint, a threshold outside 0..1 or an unknown technique name in `thresholds` aborts startup; a disabled one
loads. The result has the scores, the `detected` techniques and the `sentences` with `start`/`end` offsets in UTF-16
code units: into `Job.input.content` (`offsets_in: original`) when the source language is English and every reported
sentence is found there verbatim, else into the translation (`offsets_in: translation`). Detections are indicators, not
proof, and scores of the two models are not comparable. Add `llm: {include: true, description: ...}` (as in the
example) to let the LLM finalizer see the result. Every job that runs it makes one request to one ML service.

## Jobs from a social-media URL

`POST /api/v1/jobs` also accepts `{"scrape": {"url": ..., "max_posts": 20, "max_comments": 50}}` instead of
`input` (shared change `social-media-scrape-input`). The job answers 201 at once with `input: null` and status
`queued` or `scraping`; the orchestrator then scrapes the URL in process (see "Scraping"), maps the
post text (post URL) or the posting history, newest first (profile URL), to `input.content` and keeps the full
scrape result in `input.source.metadata.scrape`. A failed scrape ends the job `failed` with `scrape.error.code`
(for example `account_private`, `login_required`, `unsupported_url`, `scraper_unavailable`, `timeout`,
`no_text_content`) and
skips analyzers and finalization with `input_unavailable`. Requests with `scrape` answer 503
`scrape_unavailable` only when scraping is disabled (`SCRAPER_ENABLED=false`).

```
curl -s -X POST http://localhost:8080/api/v1/jobs -H 'Content-Type: application/json' \
  -d '{"scrape": {"url": "https://bsky.app/profile/alice.bsky.social/post/3kxyz", "max_posts": 10}}'
```

Follow the job with `GET /api/v1/jobs/{id}` or the event stream (`scrape.started`, then `scrape.succeeded` or
`scrape.failed`). `docs/examples/bot_detection.yaml` is a disabled example analyzer for the bot detector
service (port 8001): it binds `$input` and `$input.source`, so it receives the profile, history and comments.
Set `BOT_DETECTION_URL` (e.g. `http://bot-detector:8001/predict`), set `enabled: true` once the service accepts
the standard analyzer request, and copy the file into the directory `ANALYZERS_CONFIG` points to.

## Scraping

The scraper runs inside the orchestrator: no separate service, image or token. At startup the orchestrator
registers the Bluesky, Mastodon, Instagram, Threads and X modules (`internal/socialscraper`) over one shared HTTP
client, and a job's scrape calls them directly. Bluesky and Mastodon need no credentials. Supported URLs are
those of these five platforms; any other URL fails with `unsupported_url`.

| Variable | Default | |
|---|---|---|
| `SCRAPER_ENABLED` | `true` | `true` or `false` (any case); `false`: requests with `scrape` answer 503 `scrape_unavailable`; any other value aborts startup with a message naming the variable |
| `SCRAPER_TIMEOUT` | `120s` | Go duration; bounds one scrape, a slower scrape fails with `timeout` |
| `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` | `30` | positive number (decimals allowed); bounds every outbound platform request, a slower answer fails with `upstream_timeout` |
| `INSTAGRAM_SESSION_FILE` | unset | path of a JSON file `{"sessionid": "<value>", "csrftoken": "<value>", "ds_user_id": "<value>"}` with the cookies of an Instagram login; unset: anonymous |
| `TWITTER_AUTH_TOKEN`, `TWITTER_CT0` | unset | the `auth_token` and `ct0` cookies of an X login; both are needed for X, otherwise X URLs fail with `platform_not_configured` |

Limitations: anonymous Instagram and Threads often need a login. Instagram answers 404 to an anonymous caller even
for existing accounts, and Threads serves a page without data or with a login wall. The scrape then fails with
`login_required` (never `account_not_found` or `rate_limited`): `details.platform` names the platform and, when a
server setting would help, `details.missing_settings` lists its name (never its value), for Instagram without a
session `["INSTAGRAM_SESSION_FILE"]`. With a session file, an Instagram 404 means `account_not_found` or
`post_not_found`. Threads has no session setting: it needs a login that this scraper does not support, so its
`login_required` carries no `missing_settings`. No headless browser is used or planned; requests are plain HTTP.
Threads is read from `www.threads.com` whatever host (`threads.net` or `threads.com`) the URL names.

All settings come from the environment; no value is ever logged or returned. The orchestrator's JSON log lines
replace `Bearer ...` values, the X cookies and the session file's path and contents by `[redacted]`. A panic in a
platform module is logged (redacted, with its stack trace) and the scrape fails with `scraper_unavailable`; the
server keeps serving.

Migration notes:

- The scraper no longer runs as its own container: delete the scraper container and its Traefik route, and move
  `TWITTER_AUTH_TOKEN`, `TWITTER_CT0`, `INSTAGRAM_SESSION_FILE`, `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` and
  `SCRAPER_TIMEOUT` to the orchestrator container (same names and meaning).
- `SCRAPER_URL`, `SCRAPER_TOKEN` and `SCRAPER_LISTEN_ADDR` are ignored; if set, startup logs one warning naming
  each variable (never its value) and continues.
- The browser-based platform module is removed and no browser ships in the image: URLs of that platform now fail
  with `unsupported_url`; only the five platforms above are supported.
- `INSTAGRAM_SESSION_FILE` holds the cookie JSON shown above; a session file in an older format is not read, so
  create the new file once from a logged-in browser session. `SCRAPER_X_POOL_DB` is not read: X is read with the
  two cookie variables only and keeps no database.

Tests run offline: `go test ./...` serves recorded fixtures (`internal/socialscraper/<platform>/testdata/`) through
`internal/socialscraper/scrapertest` and reaches no platform.
