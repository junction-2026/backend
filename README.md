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
`skip_english` (default `true`), `english_min_words` (default `8`) and `english_min_ratio` (default `0.25`)
control a local English check on the text to send: text clearly English (at least `english_min_words` words and an
English function-word share of at least `english_min_ratio`) makes no Mistral call, and the result has
`source_language: "en"`, `model: "none"`, `skipped: "detected_english"` and no `usage`. Other or unsure text is translated as usual.
Cost and latency: every job whose text is not clearly English runs one extra paid LLM call (up to 60s) before
propaganda detection starts, so `translation` adds that delay and Mistral token cost to those analyses. To analyze the original text
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
the schema goes in the user message). Every reply is validated. A reply that is cut off
(`finish_reason: length`), not a JSON object or fails the schema/built-in checks is retried once with one
extra user message (never the failed reply); a second invalid reply fails only the finalization
(`invalid_output`, message `first: ...; retry: ...`). Refusals, `content_filter`, 4xx other than 429,
timeouts and cancellation are not retried. Settings (also for `topic_extraction`): `retry_invalid_output`
(default `true`), `retry_max_tokens` (default 2 x `max_tokens`, used after a cut-off reply), `retry_model`
(default empty = same model; same `base_url` and `api_key`), `retry_min_remaining` (default `5s`; no retry
with less of the `timeout` left). The result has `attempts` (1 or 2), `retry_reason` (`length`,
`invalid_json`, `schema` or null) and `usage` summed over both requests. Only analyzers with `llm: {include: true, description: ...}` reach
the LLM (see `propaganda_detection.yaml`); with none opted in, every job ends `uncertain`. Optional
`headers` and `extra_body` are sent as configured.

## Topic extraction analyzer

`docs/examples/topic_extraction.yaml` (analyzer `topic_extraction`, adapter `llm`, `execution.task:
topic_extraction`) asks any OpenAI-compatible `POST {base_url}/chat/completions` endpoint for the
fact-checkable **topics** (`title`, `query` search phrase, `description`) and **news** items (`headline`,
`summary`) and checkable **claims** in the job's `normalized_content`. The result is `{topics, news, claims, input_truncated, usage, attempts, retry_reason}`; empty
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
analyzer (`invalid_output`) after one reply retry (the `retry_*` settings of the finalizer above). One 429/5xx
answer is retried once. Add `llm: {include: true, description: ...}` to
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

## Wikipedia analyzer

`docs/examples/wikipedia.yaml` (analyzer `wikipedia`, adapter `wikipedia`, category `fact_check`,
`depends_on: [topic_extraction, translation]`) shows what Wikipedia says about the claims `topic_extraction` found,
in the post's language and in English (result shape: shared capability `wikipedia-lookup-result`). It calls no LLM
and no translation service and is not a verdict: `no_page` and `ambiguous` only mean no page was found.

| Setting | |
|---|---|
| `user_agent` | **required**: a contact URL or e-mail (`${WIKIPEDIA_CONTACT}` in the example); every request sends `JunctionOrchestrator/1.0 (<user_agent>)` per the [Wikimedia User-Agent policy](https://foundation.wikimedia.org/wiki/Policy:Wikimedia_Foundation_User-Agent_Policy) |
| `min_check_worthiness` / `max_claims` | 0.4 / 3: claims below the minimum or beyond the cap are not searched |
| `max_hits` / `max_passage_chars` | 5 (1-20) search hits considered / 1500: a longer lead is cut to a prefix |
| `max_concurrency` | 2 requests in flight per run |
| `cache_dir` / `cache_ttl` | see below / 168h |
| `base_url_template` | `https://{lang}.wikipedia.org/w/api.php`; must contain `{lang}` (tests point it at a local server) |
| `timeout` | 20s |

Each qualifying claim is searched in `{source_language}.wikipedia.org` with its `query` and in `en.wikipedia.org` with
its `query_en` (once, with `query_en`, when the source language is `en`, unknown or not a two-letter code). A claim
whose text has a digit, or whose entities contain only digits and punctuation, uses `search_mode: fulltext` (the top
hit's search snippet is the passage); otherwise `title` (an entity-titled page is preferred and its plain-text lead
is the passage). A disambiguation page is never chosen unless another entity-titled hit exists (else `ambiguous`).
`url` pins the revision that was read (`.../w/index.php?title=...&oldid=<revision_id>`). Without a qualifying claim the
analyzer is `skipped` with reason `no_checkable_claims` and sends no request. Any Wikipedia error (connection,
non-2xx after one retry of 429/5xx, undecodable answer) fails the analyzer with `adapter_error` and no result.

**Cache.** Successful answers (including `no_page` and `ambiguous`) are kept as one JSON file per
`(language, normalised query, search_mode)` in `cache_dir`, which defaults to `$DATA_DIR/wikipedia` and to
`./var/wikipedia` when `DATA_DIR` is unset. Files are written atomically and survive restarts; entries older than
`cache_ttl` and corrupt files are ignored. The container runs as uid 65532 on a distroless image with no writable data directory:
**mount a volume at `DATA_DIR` (or `cache_dir`) that is writable by uid 65532** to keep the cache across container
restarts; without a writable directory the analyzer still works but makes a request per lookup.

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
`depends_on: [translation]`, `input.bindings: {text: $analyzers.translation}`) scores the English translation for every
propaganda technique label with the NCI document model (`nci-technique`) and/or the NCI short-post model
(`nci-social`) (result shape: shared change `technique-all-labels` of `technique-classifier-result`). Add the file next
to an analyzer `translation` (see `propaganda_detection.yaml`) and set:

| Variable | |
|---|---|
| `NCI_TECHNIQUE_URL` | prediction endpoint of the document model (`nci-technique`) |
| `NCI_SOCIAL_URL` | prediction endpoint of the short-post model (`nci-social`, at most 128 tokens) |
| `ML_API_TOKEN` | sent as `Authorization: Bearer <token>` to both; never logged, returned or put in error messages |

The models are selected by the word count of `translated_text` (whitespace-separated), never by `input.source`:

| words (defaults) | models |
|---|---|
| below `technique_min_words` (40) | `nci-social` |
| `technique_min_words` to `social_max_words` (40 to 80) | `nci-technique` and `nci-social` |
| above `social_max_words` (80) | `nci-technique` |

80 words is a conservative bound for the 128 tokens of `nci-social`. Each selected model gets one
`POST {"text": [translated_text]}`; when both are selected they are called concurrently. Only the English translation
is sent, never `Job.input.content`. The answer must be `{"result": [[{label, score}, ...]], "latency_ms"?}`: one list
with each of the 18 known technique names exactly once, any other non-empty labels at most once each, and scores in
0..1, else the analyzer fails with `invalid_output`. There is no retry and no fallback to the other model: a non-2xx
answer or connection error is `adapter_error`, a slow answer `timeout` (default `timeout: 30s`), and when both models
are selected and one fails the run fails with that error and no result.

`execution` takes `social_max_words` (default 80), `technique_min_words` (default 40; at most `social_max_words + 1`
so every text gets a model) and the blocks `technique` and `social`, each with `endpoint` (required), `headers` and
`cutoff` (0..1, default 0.5 for `technique` and 0.3 for `social`); string values accept `${VAR}`. An enabled analyzer
with an unset variable, a missing block, an empty endpoint, a cutoff outside 0..1 or word bounds that leave a gap aborts
startup; a disabled one loads. The keys of the earlier version (`short`, `long`, `threshold`, `thresholds`,
`short_max_words`, `max_sentences` outside `spans`) abort startup with a message naming the replacement.

The result has `words`, `results` and `detected`. `results` has one entry per selected model (`nci-technique` first)
with its `model`, `cutoff`, `latency_ms` and `scores`: one score per known label in the fixed name order, then labels
the model returned that are not known (`known: false`, `display` = the label) in the model's order. Each score has
`label`, `display`, `score`, `model`, `cutoff`, `above_cutoff` (`score >= cutoff`) and `known`; scores below the cutoff
are kept. Scores of the two models are never averaged or merged and are not comparable. `detected` lists the known
techniques with `above_cutoff` in at least one model, in name order, with `display`, `explanation`, `models` (which
models flagged it) and `spans`; it has no score of its own. Detections are indicators, not proof. Add
`llm: {include: true, description: ...}` (as in the example) to let the LLM finalizer see the result.

### Technique spans

Each entry of `detected` also has `spans`: `{technique, text, start, end}` ranges of `Job.input.content` (the text as
submitted, not `normalized_content` and never the translation) with half-open UTF-16 offsets, so
`content.slice(start, end) === text` in JavaScript (shared change `technique-spans`). The result also has
`span_method` (`sentence` when the translation's `source_language` is `en`, else `phrase`), `span_status` (`ok`,
`failed`, `timeout`) and `spans_truncated`. They come from the optional `execution.spans` block:

| Variable | |
|---|---|
| `TECHNIQUE_SPAN_URL` | prediction endpoint of the sentence model (`nci-social`, same request and answer shape as `technique`/`social`, one list per sentence) |
| `SPAN_LLM_BASE_URL` | OpenAI-compatible base URL (`{base_url}/chat/completions`) of the phrase LLM |
| `SPAN_LLM_API_KEY` | its key, sent as `Authorization: Bearer <key>`; never logged or returned |
| `SPAN_LLM_MODEL` | its model name |

- **English** (`sentence`): `Job.input.content` is split at `.`, `!`, `?` and newlines (fragments under 3 words
  dropped); the first `spans.max_sentences` (default 20) go in one
  `POST {"text": [sentence 1..N]}` to `spans.sentence_model` (`spans_truncated` is true when more existed). A sentence
  becomes a span of each technique in `detected` it scores at or above the sentence model's `threshold` (or its
  `thresholds` override); scores of undetected techniques are ignored.
- **Other languages** (`phrase`): one LLM request with the system prompt `spans.llm.prompt_file` (default
  `/etc/orchestrator/technique_spans/prompt.md`, from `config/technique_spans/prompt.md`) and a user message with the
  content (first `spans.max_input_chars` characters, default 20000) in a `<content>` block and the detected techniques.
  The reply must be `{"phrases": [{"technique", "text"}]}`. A phrase is kept only when it is an exact, case-sensitive
  substring of `Job.input.content` (its leftmost occurrence); nothing is trimmed, normalized or translated, so a
  translated or reworded phrase is dropped. Phrases for undetected techniques are ignored, duplicates collapse, and at
  most `spans.max_phrases_per_technique` (default 5) are kept per technique (`spans_truncated` is true when more were
  dropped or the content was cut).
- No span request is sent when `detected` is empty. A technique without an accepted span stays in `detected` with
  `spans: []`. Spans are sorted by `start`, then `end`.
- The span step has its own deadline `spans.timeout` (required, shorter than `timeout`). A non-2xx answer, connection
  error or unreadable answer gives `span_status: failed`, the deadline `span_status: timeout`; either way the run
  still succeeds with every `spans` empty and the rest of the result unchanged, and the cause is only logged (with
  header values and the API key redacted). The analyzer's overall `timeout` still fails the run as before.
- `spans.sentence_model` takes `endpoint`, `headers`, `threshold` (required, 0..1) and `thresholds` (per-technique
  overrides; unchanged by the `cutoff` keys above); `spans.llm` takes
  `base_url`, `api_key`, `model`, `prompt_file`, `temperature`, `max_tokens` (default 1000), `response_format`
  (default `json_schema`), `headers` and `extra_body`, all with `${VAR}` expansion. Invalid values abort startup.
- Without a `spans` block nothing extra is requested: `span_method` follows the language, `span_status` is `ok` and
  every `spans` is `[]`.

### Plain-language explanations

A succeeded result also has `explanation_language` and `glossary_version`, every Detection a `plain_language`
`{name, definition, why}` and every span a `why` (shared change `technique-explanations-and-llm-techniques`).
`explanation_language` is the translation's `source_language` when the glossary has it (en, fi, sv, de, fr, es, ru,
uk), else `en`. Names, definitions and `why` sentences come from the fixed glossary
`internal/adapter/technique_glossary.yaml` (embedded in the binary; no LLM is asked): a span's `why` is the language's
span template with the localized name and the span text quoted verbatim, and `plain_language.why` is the first span's
`why`, or the no-span sentence (score of the whole text, no phrase located) when `spans` is empty. A glossary missing
a language, entry or template stops startup. Any wording change needs a new `version` and the pinned hash in
`technique_glossary_test.go`.

## LLM techniques analyzer

`docs/examples/llm_techniques.yaml` (analyzer `llm_techniques`, adapter `llm_techniques`, category `propaganda`,
`depends_on: [translation]`, `input.bindings: {translation: $analyzers.translation}`) asks an OpenAI-compatible LLM
for three weaknesses outside the SemEval labels: `evidence_certainty_mismatch`, `misleading_headline` and
`statistical_cherry_picking` (result shape: shared capability `llm-technique-result`). Set `LLM_TECHNIQUES_BASE_URL`,
`LLM_TECHNIQUES_API_KEY` and `LLM_TECHNIQUES_MODEL`.

- The LLM gets the system prompt `prompt_file` (default `/etc/orchestrator/llm_techniques/prompt.md`, from
  `config/llm_techniques/prompt.md`) and `Job.input.content` (first `max_input_chars` characters, default 20000) in
  `<content>`, never the translation. Only for a job created with `input` (not `scrape`) whose `source.title` is
  non-empty and differs from the content (both trimmed) is the title sent in `<title>` and `misleading_headline`
  asked for (`title_checked: true`); a scraped job's title is an account name, not a headline.
- The reply must be `{"findings": [{"label", "field", "text", "confidence", "explanation"}]}`, else one reply retry
  and then `invalid_output`. A finding is dropped when its label was not asked for, its `field` is not `title` for
  `misleading_headline` and `content` otherwise, its confidence is outside 0..1 or below `min_confidence` (default
  0.5), its explanation is blank, or its `text` is not an exact, case-sensitive substring of that field (leftmost
  occurrence, UTF-16 offsets, never repaired). At most `max_findings_per_label` (default 5) per label are kept,
  highest confidence first (`truncated` is true when more were dropped or the content was cut). Findings carry the
  glossary `name` and `definition` in `explanation_language`, the LLM's one-sentence `explanation` (credentials
  redacted), `confidence`, `source: "llm"` and the span, ordered by label, title before content, `start`, `end`.
- A non-2xx answer or connection error is `adapter_error`, the deadline `timeout` (default 30s); error messages never
  contain the API key or header values. Its failure does not affect `techniques`.
- `execution` takes only `base_url`, `api_key`, `model` (required, `${VAR}` expanded), `prompt_file`, `temperature`,
  `max_tokens` (default 1500), `headers`, `min_confidence`, `max_findings_per_label`, `max_input_chars` and
  `timeout`; any other key aborts startup, and `min_confidence`/`max_findings_per_label` are rejected on other
  adapters. The example's `llm.description` tells the finalizer that these findings are LLM-assessed, weaker than the
  `techniques` model labels and not model detections.

## Jobs from a social-media URL

`POST /api/v1/jobs` also accepts `{"scrape": {"url": ..., "max_posts": 20, "max_comments": 50}}` instead of
`input` (shared change `social-media-scrape-input`). The job answers 201 at once with `input: null` and status
`queued` or `scraping`; the orchestrator then scrapes the URL in process (see "Scraping"), maps the
post text (post URL), the posting history, newest first (profile URL) or the extracted article text (any other web
page, `url_kind: page`) to `input.content` and keeps the full
scrape result in `input.source.metadata.scrape`. A failed scrape ends the job `failed` with `scrape.error.code`
(for example `account_private`, `account_not_found`, `post_not_found`, `rate_limited`, `platform_not_configured`,
`upstream_error`, `upstream_timeout`, `unsupported_url`, `login_required`, `scraper_unavailable`, `timeout`,
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
registers the Bluesky, Mastodon and web modules (`internal/socialscraper`) over shared HTTP clients, and a job's
scrape calls them directly. Routing needs no network access: `bsky.app/profile/<handle or DID>[/post/<rkey>]` is
Bluesky; the Mastodon shapes `/@<user>[/<status id>]` and `/users/<user>[/statuses/<status id>]` on any other host
are probed as Mastodon; **every other http(s) URL is a `web` page** (`platform: web`, `url_kind: page`), including
links to other social networks and a Mastodon-shaped URL of a host that does not serve the Mastodon API. Only URLs
that cannot be fetched safely fail with `unsupported_url` without any request: not http(s), malformed, with
credentials (`user:password@`), longer than 2,000 characters, or naming `localhost`, a `*.localhost` name or a private,
loopback, link-local or unspecified IP literal.

### Web pages (Oxylabs)

A `web` page is fetched by the paid scraping provider [Oxylabs](https://oxylabs.io), never by the server itself.
The main text is extracted with go-trafilatura (paragraphs joined by `\n`, at most 20,000 characters, `truncated`
true when cut) together with metadata (`title`, `site_name`, `author`, `published_at`, `lang`, `canonical_url`,
`host`, `domain`) in the job's `input.source.metadata.scrape`; `input.source.url` is the canonical URL when it is on
the page's own registered domain, otherwise the final URL, so `source_info` can name the page's outlet.

| Variable | Default | |
|---|---|---|
| `OXYLABS_API` | `web_api` | `web_api` (Oxylabs Web API) or `web_scraper_api` (Web Scraper API, `source: universal`) |
| `OXYLABS_WEB_API_KEY` | | required for `web_api` (sent as a bearer token) |
| `OXYLABS_USERNAME`, `OXYLABS_PASSWORD` | | required for `web_scraper_api` (HTTP Basic) |
| `OXYLABS_LOCATION` | | optional two-letter country code the page is fetched from |
| `WEB_MIN_TEXT_CHARS` | `300` | extracted text shorter than this triggers a rendered fetch, the Markdown fallback or the login-wall check |
| `WEB_CACHE_TTL` | `1h` | Go duration; successful pages are cached in memory by normalized URL (failures never) |
| `WEB_TIMEOUT_SECONDS` | `max(SCRAPER_UPSTREAM_TIMEOUT_SECONDS, 150)` | bound of a rendered (JavaScript) fetch |

Without the credentials the orchestrator still starts; a web scrape then fails with `platform_not_configured` and
`details.missing_settings` naming the variable, before any request. Invalid values of the variables above abort
startup with a message naming the variable.

Cost controls: a scrape first fetches without JavaScript and fetches once more with rendering only when the extracted
text is shorter than `WEB_MIN_TEXT_CHARS`; a 429 or 5xx answer of the provider is retried at most once; so a scrape
sends at most 3 provider requests. The result records `rendered`, `cached` and `oxylabs_requests`; a cached page sends
none. Login walls fail with `login_required`, an empty page with `no_text_content`, a page that answered 4xx/5xx
with `upstream_error` and `details.target_status`; login walls are never bypassed. **Tests never call Oxylabs:** they
use a stub provider and recorded HTML fixtures (`internal/socialscraper/web/testdata`).

Other settings of the scraper:

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
`skip_english` (default `true`), `english_min_words` (default `8`) and `english_min_ratio` (default `0.25`)
control a local English check on the text to send: text clearly English (at least `english_min_words` words and an
English function-word share of at least `english_min_ratio`) makes no Mistral call, and the result has
`source_language: "en"`, `model: "none"`, `skipped: "detected_english"` and no `usage`. Other or unsure text is translated as usual.
Cost and latency: every job whose text is not clearly English runs one extra paid LLM call (up to 60s) before
propaganda detection starts, so `translation` adds that delay and Mistral token cost to those analyses. To analyze the original text
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
the schema goes in the user message). Every reply is validated. A reply that is cut off
(`finish_reason: length`), not a JSON object or fails the schema/built-in checks is retried once with one
extra user message (never the failed reply); a second invalid reply fails only the finalization
(`invalid_output`, message `first: ...; retry: ...`). Refusals, `content_filter`, 4xx other than 429,
timeouts and cancellation are not retried. Settings (also for `topic_extraction`): `retry_invalid_output`
(default `true`), `retry_max_tokens` (default 2 x `max_tokens`, used after a cut-off reply), `retry_model`
(default empty = same model; same `base_url` and `api_key`), `retry_min_remaining` (default `5s`; no retry
with less of the `timeout` left). The result has `attempts` (1 or 2), `retry_reason` (`length`,
`invalid_json`, `schema` or null) and `usage` summed over both requests. Only analyzers with `llm: {include: true, description: ...}` reach
the LLM (see `propaganda_detection.yaml`); with none opted in, every job ends `uncertain`. Optional
`headers` and `extra_body` are sent as configured.

## Topic extraction analyzer

`docs/examples/topic_extraction.yaml` (analyzer `topic_extraction`, adapter `llm`, `execution.task:
topic_extraction`) asks any OpenAI-compatible `POST {base_url}/chat/completions` endpoint for the
fact-checkable **topics** (`title`, `query` search phrase, `description`) and **news** items (`headline`,
`summary`) and checkable **claims** in the job's `normalized_content`. The result is `{topics, news, claims, input_truncated, usage, attempts, retry_reason}`; empty
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
analyzer (`invalid_output`) after one reply retry (the `retry_*` settings of the finalizer above). One 429/5xx
answer is retried once. Add `llm: {include: true, description: ...}` to
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
`depends_on: [translation]`, `input.bindings: {text: $analyzers.translation}`) scores the English translation for every
propaganda technique label with the NCI document model (`nci-technique`) and/or the NCI short-post model
(`nci-social`) (result shape: shared change `technique-all-labels` of `technique-classifier-result`). Add the file next
to an analyzer `translation` (see `propaganda_detection.yaml`) and set:

| Variable | |
|---|---|
| `NCI_TECHNIQUE_URL` | prediction endpoint of the document model (`nci-technique`) |
| `NCI_SOCIAL_URL` | prediction endpoint of the short-post model (`nci-social`, at most 128 tokens) |
| `ML_API_TOKEN` | sent as `Authorization: Bearer <token>` to both; never logged, returned or put in error messages |

The models are selected by the word count of `translated_text` (whitespace-separated), never by `input.source`:

| words (defaults) | models |
|---|---|
| below `technique_min_words` (40) | `nci-social` |
| `technique_min_words` to `social_max_words` (40 to 80) | `nci-technique` and `nci-social` |
| above `social_max_words` (80) | `nci-technique` |

80 words is a conservative bound for the 128 tokens of `nci-social`. Each selected model gets one
`POST {"text": [translated_text]}`; when both are selected they are called concurrently. Only the English translation
is sent, never `Job.input.content`. The answer must be `{"result": [[{label, score}, ...]], "latency_ms"?}`: one list
with each of the 18 known technique names exactly once, any other non-empty labels at most once each, and scores in
0..1, else the analyzer fails with `invalid_output`. There is no retry and no fallback to the other model: a non-2xx
answer or connection error is `adapter_error`, a slow answer `timeout` (default `timeout: 30s`), and when both models
are selected and one fails the run fails with that error and no result.

`execution` takes `social_max_words` (default 80), `technique_min_words` (default 40; at most `social_max_words + 1`
so every text gets a model) and the blocks `technique` and `social`, each with `endpoint` (required), `headers` and
`cutoff` (0..1, default 0.5 for `technique` and 0.3 for `social`); string values accept `${VAR}`. An enabled analyzer
with an unset variable, a missing block, an empty endpoint, a cutoff outside 0..1 or word bounds that leave a gap aborts
startup; a disabled one loads. The keys of the earlier version (`short`, `long`, `threshold`, `thresholds`,
`short_max_words`, `max_sentences` outside `spans`) abort startup with a message naming the replacement.

The result has `words`, `results` and `detected`. `results` has one entry per selected model (`nci-technique` first)
with its `model`, `cutoff`, `latency_ms` and `scores`: one score per known label in the fixed name order, then labels
the model returned that are not known (`known: false`, `display` = the label) in the model's order. Each score has
`label`, `display`, `score`, `model`, `cutoff`, `above_cutoff` (`score >= cutoff`) and `known`; scores below the cutoff
are kept. Scores of the two models are never averaged or merged and are not comparable. `detected` lists the known
techniques with `above_cutoff` in at least one model, in name order, with `display`, `explanation`, `models` (which
models flagged it) and `spans`; it has no score of its own. Detections are indicators, not proof. Add
`llm: {include: true, description: ...}` (as in the example) to let the LLM finalizer see the result.

### Technique spans

Each entry of `detected` also has `spans`: `{technique, text, start, end}` ranges of `Job.input.content` (the text as
submitted, not `normalized_content` and never the translation) with half-open UTF-16 offsets, so
`content.slice(start, end) === text` in JavaScript (shared change `technique-spans`). The result also has
`span_method` (`sentence` when the translation's `source_language` is `en`, else `phrase`), `span_status` (`ok`,
`failed`, `timeout`) and `spans_truncated`. They come from the optional `execution.spans` block:

| Variable | |
|---|---|
| `TECHNIQUE_SPAN_URL` | prediction endpoint of the sentence model (`nci-social`, same request and answer shape as `technique`/`social`, one list per sentence) |
| `SPAN_LLM_BASE_URL` | OpenAI-compatible base URL (`{base_url}/chat/completions`) of the phrase LLM |
| `SPAN_LLM_API_KEY` | its key, sent as `Authorization: Bearer <key>`; never logged or returned |
| `SPAN_LLM_MODEL` | its model name |

- **English** (`sentence`): `Job.input.content` is split at `.`, `!`, `?` and newlines (fragments under 3 words
  dropped); the first `spans.max_sentences` (default 20) go in one
  `POST {"text": [sentence 1..N]}` to `spans.sentence_model` (`spans_truncated` is true when more existed). A sentence
  becomes a span of each technique in `detected` it scores at or above the sentence model's `threshold` (or its
  `thresholds` override); scores of undetected techniques are ignored.
- **Other languages** (`phrase`): one LLM request with the system prompt `spans.llm.prompt_file` (default
  `/etc/orchestrator/technique_spans/prompt.md`, from `config/technique_spans/prompt.md`) and a user message with the
  content (first `spans.max_input_chars` characters, default 20000) in a `<content>` block and the detected techniques.
  The reply must be `{"phrases": [{"technique", "text"}]}`. A phrase is kept only when it is an exact, case-sensitive
  substring of `Job.input.content` (its leftmost occurrence); nothing is trimmed, normalized or translated, so a
  translated or reworded phrase is dropped. Phrases for undetected techniques are ignored, duplicates collapse, and at
  most `spans.max_phrases_per_technique` (default 5) are kept per technique (`spans_truncated` is true when more were
  dropped or the content was cut).
- No span request is sent when `detected` is empty. A technique without an accepted span stays in `detected` with
  `spans: []`. Spans are sorted by `start`, then `end`.
- The span step has its own deadline `spans.timeout` (required, shorter than `timeout`). A non-2xx answer, connection
  error or unreadable answer gives `span_status: failed`, the deadline `span_status: timeout`; either way the run
  still succeeds with every `spans` empty and the rest of the result unchanged, and the cause is only logged (with
  header values and the API key redacted). The analyzer's overall `timeout` still fails the run as before.
- `spans.sentence_model` takes `endpoint`, `headers`, `threshold` (required, 0..1) and `thresholds` (per-technique
  overrides; unchanged by the `cutoff` keys above); `spans.llm` takes
  `base_url`, `api_key`, `model`, `prompt_file`, `temperature`, `max_tokens` (default 1000), `response_format`
  (default `json_schema`), `headers` and `extra_body`, all with `${VAR}` expansion. Invalid values abort startup.
- Without a `spans` block nothing extra is requested: `span_method` follows the language, `span_status` is `ok` and
  every `spans` is `[]`.

### Plain-language explanations

A succeeded result also has `explanation_language` and `glossary_version`, every Detection a `plain_language`
`{name, definition, why}` and every span a `why` (shared change `technique-explanations-and-llm-techniques`).
`explanation_language` is the translation's `source_language` when the glossary has it (en, fi, sv, de, fr, es, ru,
uk), else `en`. Names, definitions and `why` sentences come from the fixed glossary
`internal/adapter/technique_glossary.yaml` (embedded in the binary; no LLM is asked): a span's `why` is the language's
span template with the localized name and the span text quoted verbatim, and `plain_language.why` is the first span's
`why`, or the no-span sentence (score of the whole text, no phrase located) when `spans` is empty. A glossary missing
a language, entry or template stops startup. Any wording change needs a new `version` and the pinned hash in
`technique_glossary_test.go`.

## LLM techniques analyzer

`docs/examples/llm_techniques.yaml` (analyzer `llm_techniques`, adapter `llm_techniques`, category `propaganda`,
`depends_on: [translation]`, `input.bindings: {translation: $analyzers.translation}`) asks an OpenAI-compatible LLM
for three weaknesses outside the SemEval labels: `evidence_certainty_mismatch`, `misleading_headline` and
`statistical_cherry_picking` (result shape: shared capability `llm-technique-result`). Set `LLM_TECHNIQUES_BASE_URL`,
`LLM_TECHNIQUES_API_KEY` and `LLM_TECHNIQUES_MODEL`.

- The LLM gets the system prompt `prompt_file` (default `/etc/orchestrator/llm_techniques/prompt.md`, from
  `config/llm_techniques/prompt.md`) and `Job.input.content` (first `max_input_chars` characters, default 20000) in
  `<content>`, never the translation. Only for a job created with `input` (not `scrape`) whose `source.title` is
  non-empty and differs from the content (both trimmed) is the title sent in `<title>` and `misleading_headline`
  asked for (`title_checked: true`); a scraped job's title is an account name, not a headline.
- The reply must be `{"findings": [{"label", "field", "text", "confidence", "explanation"}]}`, else one reply retry
  and then `invalid_output`. A finding is dropped when its label was not asked for, its `field` is not `title` for
  `misleading_headline` and `content` otherwise, its confidence is outside 0..1 or below `min_confidence` (default
  0.5), its explanation is blank, or its `text` is not an exact, case-sensitive substring of that field (leftmost
  occurrence, UTF-16 offsets, never repaired). At most `max_findings_per_label` (default 5) per label are kept,
  highest confidence first (`truncated` is true when more were dropped or the content was cut). Findings carry the
  glossary `name` and `definition` in `explanation_language`, the LLM's one-sentence `explanation` (credentials
  redacted), `confidence`, `source: "llm"` and the span, ordered by label, title before content, `start`, `end`.
- A non-2xx answer or connection error is `adapter_error`, the deadline `timeout` (default 30s); error messages never
  contain the API key or header values. Its failure does not affect `techniques`.
- `execution` takes only `base_url`, `api_key`, `model` (required, `${VAR}` expanded), `prompt_file`, `temperature`,
  `max_tokens` (default 1500), `headers`, `min_confidence`, `max_findings_per_label`, `max_input_chars` and
  `timeout`; any other key aborts startup, and `min_confidence`/`max_findings_per_label` are rejected on other
  adapters. The example's `llm.description` tells the finalizer that these findings are LLM-assessed, weaker than the
  `techniques` model labels and not model detections.

## Jobs from a social-media URL

`POST /api/v1/jobs` also accepts `{"scrape": {"url": ..., "max_posts": 20, "max_comments": 50}}` instead of
`input` (shared change `social-media-scrape-input`). The job answers 201 at once with `input: null` and status
`queued` or `scraping`; the orchestrator then scrapes the URL in process (see "Scraping"), maps the
post text (post URL), the posting history, newest first (profile URL) or the extracted article text (any other web
page, `url_kind: page`) to `input.content` and keeps the full
scrape result in `input.source.metadata.scrape`. A failed scrape ends the job `failed` with `scrape.error.code`
(for example `account_private`, `account_not_found`, `post_not_found`, `rate_limited`, `platform_not_configured`,
`upstream_error`, `upstream_timeout`, `unsupported_url`, `login_required`, `scraper_unavailable`, `timeout`,
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

## Live progress events

The event stream (`GET /api/v1/jobs/{id}/events`, shared change `live-analyzer-progress`) carries a run plan,
user-facing activities, progress counters and compact result previews, so a client can show a live progress view
without reading full results. Every member is additive; `GET /api/v1/jobs/{id}` and the Job object do not change.

- `analysis.planned` `{analyzers: [PlanEntry], total}`: directly after `job.started` (text jobs) or `scrape.succeeded`
  (scrape jobs), once; never for a failed scrape or after cancellation. A PlanEntry is
  `{id, name, activity_key, depends_on, phase}`; `phase` is 1 + the highest planned dependency phase, the finalizer
  (`depends_on: ["*"]`, `activity_key: "finalizer"`) comes last. Disabled, not-selected and hidden analyzers are not
  planned; the finalizer is planned when finalization will run.
- **Activity** `{key, text, detail, params}` on `analyzer.started`, `analysis.finalizing` and `scrape.started`:
  fixed English `text` from a built-in table (unknown keys use the analyzer name), `detail` and `params` from facts
  known at start, never from an LLM or translation call. `news_search`: `params.languages`, detail "in German and
  English"; `fact_check`: `params.claims`; `techniques`: `params.models` ("document model", "short-post model");
  `llm_techniques`: `params.title_checked`; `scrape`: `params.host`. A fact that cannot be determined is left out
  and `detail` is null.
- **Progress** `{done, running, total}` on `analyzer.started`, every settle event after the plan,
  `analysis.finalizing` (`done == total - 1`), `analysis.finalized` and `analysis.finalization_failed`
  (`done == total`). `done` grows by one per settle event of a planned analyzer.
- `analyzer.failed` and `analyzer.skipped` also carry `availability` and `note` (equal to `data.analyzer`'s);
  `analyzer.succeeded` carries `preview`.
- **Previews** are built in code from the result (null for other analyzers, such as `text_stats`):
  `translation` `{source_language, translated}`; `propaganda_detection` `{label, score}`; `topic_extraction`
  `{claims, topics, claims_total, topics_total}` (first 5 texts); `news_search` `{articles, outlets (max 6),
  languages}`; `fact_check` `{fact_checks, top (first 3: publisher, rating, rating_normalized, url)}`; `source_info`
  `{summary, page_outlet}`; `techniques` `{models, explanation_language, glossary_version, detected_total, detected}`
  with at most 8 `{label, name, definition, why, score, models, spans (max 3: start, end, text, why)}`, best score
  first; `llm_techniques` `{source: "llm", title_checked, explanation_language, glossary_version, findings_total,
  findings}` with at most 6 findings of the three closed labels by confidence, each with `span {field, start, end,
  text}`. Offsets are UTF-16 offsets into `input.content` (or `input.source.title` for `field: "title"`). A missing
  or wrong-typed result member yields null (`[]` for lists); a preview that cannot be built is null.
- `job.snapshot` carries `data.job` plus `plan`, `progress` (both null before the plan), `previews` (succeeded
  planned analyzers) and `activities` (running planned analyzers and the running finalizer), equal to what the
  events carried.

Registry: an analyzer entry takes an optional `ui: {hidden: false, activity_key: <id>}`. Hidden analyzers keep their
run and events but are left out of the plan, progress, previews and activities; `text_stats` is hidden unless its
entry sets `ui.hidden: false`. `ui` is not shown by `GET /api/v1/analyzers`.

## Scraping

The scraper runs inside the orchestrator: no separate service, image or token. At startup the orchestrator
registers the Bluesky and Mastodon modules (`internal/socialscraper`) over one shared HTTP client, and a job's
scrape calls them directly. Both use public APIs and need no credentials. Supported URLs are
`bsky.app/profile/<handle or DID>[/post/<rkey>]` and the Mastodon shapes `/@<user>[/<status id>]` and
`/users/<user>[/statuses/<status id>]` on any other host that serves the Mastodon REST API. Any other URL,
including links to other social networks, fails with `unsupported_url` without a request.

| Variable | Default | |
|---|---|---|
| `SCRAPER_ENABLED` | `true` | `true` or `false` (any case); `false`: requests with `scrape` answer 503 `scrape_unavailable`; any other value aborts startup with a message naming the variable |
| `SCRAPER_TIMEOUT` | `360s` | Go duration; bounds one scrape, a slower scrape fails with `timeout`. The default fits an unrendered page fetch (30 s), a rendered one (150 s) and one retry (150 s); lower it only if you also lower `WEB_TIMEOUT_SECONDS` |
| `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` | `30` | positive number (decimals allowed); bounds every outbound platform request, a slower answer fails with `upstream_timeout` |

All settings come from the environment; no value is ever logged or returned. The orchestrator's JSON log lines
replace `Bearer ...` values and the Oxylabs credentials by `[redacted]`. A panic in a platform module is logged (redacted, with its stack
trace) and the scrape fails with `scraper_unavailable`; the server keeps serving.

Migration notes:

- The scraper no longer runs as its own container: delete the scraper container and its Traefik route, and move
  `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` and `SCRAPER_TIMEOUT` to the orchestrator container (same names and meaning).
- `SCRAPER_URL`, `SCRAPER_TOKEN` and `SCRAPER_LISTEN_ADDR` are ignored; if set, startup logs one warning naming
  each variable (never its value) and continues.
- Only Bluesky and Mastodon have platform modules (junction-2026/project#24); every other URL is scraped as a web
  page through Oxylabs (junction-2026/project#26) when `OXYLABS_*` is configured, and the former platform credential
  variables are no longer read: remove them from deployments. `SCRAPER_TIMEOUT` now defaults to `360s` (was `120s`).
  No browser-based platform module exists and no browser ships in the image.

Tests run offline: `go test ./...` serves recorded fixtures (`internal/socialscraper/<platform>/testdata/`) through
`internal/socialscraper/scrapertest` and reaches no platform.
