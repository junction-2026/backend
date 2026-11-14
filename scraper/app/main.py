"""social-scraper-api: GET /health, POST /scrape.

Run: uvicorn app.main:create_app --factory --host 0.0.0.0 --port 8003
"""
import asyncio
import hmac
import logging
import time

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from pydantic import ValidationError
from starlette.exceptions import HTTPException as StarletteHTTPException

from . import registry
from .config import Settings, load_settings
from .errors import ScrapeError
from .logging_setup import setup as setup_logging
from .models import ScrapeBody, ScrapeResult
from .platforms.base import ScrapeRequest
from .textutil import now_rfc3339

log = logging.getLogger("scraper")


def _error(e: ScrapeError) -> JSONResponse:
    return JSONResponse(e.envelope(), status_code=e.status, headers=e.headers)


def _authorized(request: Request, token: str) -> bool:
    scheme, _, supplied = request.headers.get("authorization", "").partition(" ")
    return scheme.lower() == "bearer" and bool(supplied) and hmac.compare_digest(supplied.encode(), token.encode())


def _field_messages(e: ValidationError) -> dict[str, str]:
    return {str(err["loc"][0]) if err["loc"] else "body": err["msg"] for err in e.errors()}


def create_app(settings: Settings | None = None) -> FastAPI:
    settings = settings or load_settings()
    setup_logging()
    app = FastAPI(title="social-scraper-api", docs_url=None, redoc_url=None, openapi_url=None)

    @app.get("/health")
    async def health():
        return {"status": "ok"}

    @app.post("/scrape")
    async def scrape(request: Request):
        # Auth first: the body is not read and no platform is contacted for a bad token.
        if not _authorized(request, settings.token):
            return _error(ScrapeError("unauthorized", "missing or invalid bearer token",
                                      headers={"WWW-Authenticate": "Bearer"}))
        started, platform = time.monotonic(), None
        try:
            try:
                raw = await request.json()
            except ValueError:
                raise ScrapeError("invalid_request", "the request body is not valid JSON") from None
            if not isinstance(raw, dict):
                raise ScrapeError("invalid_request", "the request body must be a JSON object")
            try:
                body = ScrapeBody.model_validate(raw)
            except ValidationError as ve:
                raise ScrapeError("validation_failed", "the request body is invalid", fields=_field_messages(ve)) from None
            module, rec = registry.resolve(body.url)
            platform = rec.platform
            req = ScrapeRequest(body.url, body.max_posts, body.max_comments, rec, settings.upstream_timeout_s)
            try:
                async with asyncio.timeout(settings.upstream_timeout_s):
                    result: ScrapeResult = await module.scrape(req)
            except TimeoutError:
                raise ScrapeError("upstream_timeout", "the platform did not answer in time", platform=platform) from None
            result.scraped_at = now_rfc3339()
            result.latency_ms = int((time.monotonic() - started) * 1000)
            log.info("scrape ok platform=%s kind=%s posts=%d comments=%d ms=%d", platform, rec.url_kind,
                     len(result.posts), len(result.comments), result.latency_ms)
            return JSONResponse(result.model_dump(mode="json"))
        except ScrapeError as e:
            if platform and "platform" not in e.details:
                e.details["platform"] = platform
            log.info("scrape failed platform=%s code=%s", platform, e.code)
            return _error(e)
        except Exception:
            log.exception("scrape crashed platform=%s", platform)  # traceback goes to the (redacted) log only
            return _error(ScrapeError("internal_error", "unexpected internal error", platform=platform))

    @app.exception_handler(StarletteHTTPException)
    async def _http_error(request: Request, exc: StarletteHTTPException):
        # unknown path / wrong method: same envelope, status kept
        body = {"error": {"code": "invalid_request", "message": str(exc.detail)}}
        return JSONResponse(body, status_code=exc.status_code, headers=getattr(exc, "headers", None))

    @app.exception_handler(Exception)
    async def _unhandled(request: Request, exc: Exception):
        log.error("unhandled %s", type(exc).__name__)
        return _error(ScrapeError("internal_error", "unexpected internal error"))

    return app
