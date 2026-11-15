# Public analysis API

For social media platforms and news outlets: send a post, comments or both (or a URL) and poll a small JSON summary
of the analysis. No login, no API key, no cookies; credential headers are ignored. Contract: shared-specs capability
`public-analysis-api`.

Each request creates one ordinary analysis job (all analyzers, final assessment on). Its `id` also works on
`GET /api/v1/jobs/{id}` and the web result page `/jobs/{id}`.

## Submit: `POST /api/v1/public/analyses`

Body (JSON object, at most 1 MiB; unknown members and `null` members are ignored):

| Member | Type | Meaning |
|---|---|---|
| `post` | string | The post text. |
| `text` | string | Alias of `post`; do not send both. |
| `comments` | string or array of at most 100 strings | Comments; blank ones are dropped. |
| `url` | string | Absolute `http`/`https` URL (≤ 2048 characters) to scrape instead of pasted text. Not combinable with a non-blank post or comment. |
| `public` | boolean, default `true` | `false` keeps the job out of the public history listing. |

A post alone is analysed exactly as sent. Otherwise the texts become one input:

```
Post:
<trimmed post>

Comment 1:
<trimmed comment>

Comment 2:
...
```

```sh
curl -s -X POST https://veil.example.org/api/v1/public/analyses \
  -H 'Content-Type: application/json' \
  -d '{"post": "Vote no!", "comments": ["Agreed", "Liar!"]}'
```

`201 Created`, `Location: /api/v1/public/analyses/<id>`, body a PublicAnalysis (below) with `done: false`.

## Get: `GET /api/v1/public/analyses/{id}`

`200` with the PublicAnalysis of any job id, `404 not_found` for an unknown id.

```json
{
  "id": "6f0c...",
  "status": "completed",
  "done": true,
  "created_at": "2026-11-15T10:00:00Z",
  "finished_at": "2026-11-15T10:00:41Z",
  "coverage": {"total": 12, "applicable": 11, "pending": 0, "running": 0, "succeeded": 10, "failed": 1, "skipped": 1, "settled": 12},
  "input": {"content": "Post:\nVote no!\n\nComment 1:\nAgreed", "url": null},
  "finalization_status": "completed",
  "result": {"verdict": "detected", "confidence": 0.86, "user_text": "This post shows signs of propaganda.", "produced_by": "finalizer"},
  "techniques": [
    {"source": "classifier", "label": "Doubt", "name": "Doubt", "score": 0.71, "strength": "strong",
     "spans": [{"field": "content", "start": 0, "end": 12, "text": "Is it true?!"}]},
    {"source": "llm", "label": "statistical_cherry_picking", "name": "Cherry-picked statistic", "score": 0.6, "strength": null,
     "spans": [{"field": "content", "start": 10, "end": 13, "text": "90%"}]}
  ],
  "bot_signal": {"label": "bot_or_generated", "score": 0.87, "scope": "text"},
  "evidence": {
    "fact_checks": [{"claim": "...", "for_claim": "...", "rating": "Falsch", "rating_normalized": "false", "publisher": "Correctiv", "url": "https://correctiv.org/x", "review_date": "2025-03-04T00:00:00Z"}],
    "news": [{"title": "...", "url": "https://...", "domain": "example.com", "source": null, "date": null}],
    "wikipedia": [{"claim": "...", "language": "en", "title": "Retirement age", "url": "https://en.wikipedia.org/w/index.php?...", "passage": "...", "coverage": "aligned"}]
  },
  "error": null,
  "share_url": "https://veil.example.org/jobs/6f0c..."
}
```

Every member is always present; read unknown members as additions and ignore them.

- `result` is null until the final assessment completed (see `finalization_status`). `confidence` is not a
  probability that the content is propaganda; an unknown `verdict` reads as `uncertain`.
- `techniques`, `bot_signal` and each `evidence` list are null until their analyzer succeeded (they can appear
  before `done`). `null` means "no lookup available", `[]` means "nothing found"; neither says a claim is true or false.
- Key techniques by `source` + `label`; do not compare `score` across sources; a `weak` `strength` is a hint.
- `bot_signal` describes the text only, never the author or account; `score` is always the probability of
  `bot_or_generated`.
- `error` is set only for `status: failed`: the scrape error (e.g. `account_private`) or `internal_error`.
- Span offsets are UTF-16 offsets into `input.content` (or the source title for `field: "title"`).

### Polling

Poll `GET` until `done` is `true`; every 2–5 seconds is enough (analyses take tens of seconds). Stay within the get
rate limit below and back off by `Retry-After` on 429.

## Errors

Envelope `{"error": {"code", "message", "details"?}}`. Submit checks in this order; none creates a job:

| Status | `error.code` | When |
|---|---|---|
| 429 | `rate_limited` | Global limit exceeded; `Retry-After` header = `error.details.retry_after_s` (≥ 1). |
| 413 | `payload_too_large` | Body over 1 MiB. |
| 400 | `invalid_request` | Empty body, invalid JSON or not an object. |
| 422 | `validation_failed` | Wrong member type, `post` and `text` together, > 100 comments, bad or combined `url`; `error.details.fields` keyed by member (`post`, `text`, `comments`, `comments[1]`, `url`, `public`). |
| 400 | `missing_content` | No non-blank post, text or comment and no url. |
| 503 | `scrape_unavailable` | `url` while scraping is disabled on this server. |

Get answers 429 `rate_limited` or 404 `not_found`; other methods get 405 `method_not_allowed`.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `PUBLIC_API_SUBMIT_PER_MINUTE` | `30` | Global submit limit (one token bucket for all clients; burst = value, refill value/60 per second). |
| `PUBLIC_API_GET_PER_MINUTE` | `600` | Separate global get limit, so polling does not use up submissions. |
| `PUBLIC_BASE_URL` | unset | The web client's absolute `http(s)` base URL; `share_url` is `<base>/jobs/<id>`. Unset or empty: `share_url` is null. |

A non-integer or value below 1, or a `PUBLIC_BASE_URL` that is not an absolute http(s) URL, stops startup with an
error naming the variable. Every request counts, also rejected ones. `/api/v1/jobs` is not limited.
