# Social-media scraper service

Python 3.12 FastAPI service that implements the shared `social-scraper-api`: given the URL of a public
post or profile it returns the author's profile, recent posts and (for post URLs) the comments, normalized
across platforms, with typed errors. Platforms: Bluesky and Mastodon here; Instagram, Threads, Facebook and
X are recognized and answer `503 platform_not_configured` until their modules are added.

- `GET /health` – `{"status": "ok"}`, no auth, contacts no platform.
- `POST /scrape` – `Authorization: Bearer <SCRAPER_TOKEN>`, body `{"url", "max_posts" (1-100, default 20), "max_comments" (0-200, default 50)}`.

## Environment

All configuration comes from the environment; values are never logged and the log redacts `Authorization` headers.

| Variable | Required | Meaning |
|---|---|---|
| `SCRAPER_TOKEN` | yes | Bearer token clients must send. The service refuses to start without it. |
| `SCRAPER_UPSTREAM_TIMEOUT_SECONDS` | no (30) | Bound for one whole scrape and for every upstream request; exceeding it answers `504 upstream_timeout`. |
| platform credentials | per platform | Read by the platform modules that need them (for example the X cookie settings). Bluesky and Mastodon need none. |

## Run locally

```sh
cd scraper
python3.12 -m venv .venv && . .venv/bin/activate
pip install -e ".[dev]"
SCRAPER_TOKEN=change-me uvicorn app.main:create_app --factory --host 0.0.0.0 --port 8003
curl -s localhost:8003/health
curl -s localhost:8003/scrape -H 'Authorization: Bearer change-me' \
  -d '{"url": "https://bsky.app/profile/bsky.app", "max_posts": 3}'
pytest            # offline: recorded JSON fixtures + HTTP mock, no real platform is contacted
```

## Run in Docker (port 8003, network `ml_net`)

```sh
docker build -t junction-scraper scraper/
docker run -d --name scraper --network ml_net -p 8003:8003 \
  --env-file /path/to/scraper.env junction-scraper      # scraper.env holds SCRAPER_TOKEN=... (keep it out of git)
```

Behind Traefik the operator adds labels like these (host name and cert resolver are placeholders; use your own):

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.scraper.rule=Host(`scraper.example.com`)
  - traefik.http.routers.scraper.entrypoints=websecure
  - traefik.http.routers.scraper.tls.certresolver=letsencrypt
  - traefik.http.services.scraper.loadbalancer.server.port=8003
```

## Adding a platform

A platform module has `NAME`, `recognize(url: NormalizedUrl) -> Recognized | None` and
`async scrape(request: ScrapeRequest) -> ScrapeResult` (see `app/platforms/base.py`). Put it in
`app/platforms/<name>.py`, then replace the matching `Pending` entry in the `MODULES` list of
`app/registry.py`; no other core file changes. Raise `ScrapeError(code, message, platform=...)` for typed failures;
any other exception becomes `500 internal_error` without details.
