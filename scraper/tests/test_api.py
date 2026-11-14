import asyncio
import logging

import pytest
from fastapi.testclient import TestClient

from app import registry
from app.config import Settings, load_settings
from app.logging_setup import redact
from app.main import create_app

from .conftest import AUTH, TOKEN, scrape

ERROR_CODES = {"unauthorized", "invalid_request", "validation_failed", "unsupported_url", "internal_error"}


def test_health_without_auth(client):
    r = client.get("/health")
    assert r.status_code == 200 and r.json()["status"] == "ok"


@pytest.mark.parametrize("headers", [{}, {"Authorization": "Bearer wrong"}, {"Authorization": TOKEN},
                                     {"Authorization": "Basic " + TOKEN}, {"Authorization": "Bearer "}])
def test_unauthorized(client, headers):
    r = client.post("/scrape", json={"url": "https://bsky.app/profile/alice.bsky.social"}, headers=headers)
    assert r.status_code == 401
    assert r.json()["error"]["code"] == "unauthorized"
    assert r.headers["www-authenticate"] == "Bearer"
    assert TOKEN not in r.text


def test_auth_checked_before_body(client):
    r = client.post("/scrape", content=b"{not json", headers={"Authorization": "Bearer wrong"})
    assert r.status_code == 401


def test_invalid_json(client):
    r = client.post("/scrape", content=b"{not json", headers=AUTH)
    assert r.status_code == 400 and r.json()["error"]["code"] == "invalid_request"
    r = client.post("/scrape", json=["x"], headers=AUTH)
    assert r.status_code == 400


@pytest.mark.parametrize("body,field", [
    ({}, "url"),
    ({"url": 5}, "url"),
    ({"url": "not a url"}, "url"),
    ({"url": "/relative/path"}, "url"),
    ({"url": "ftp://bsky.app/profile/a"}, "url"),
    ({"url": "https://example.com/" + "a" * 2048}, "url"),
    ({"url": "https://bsky.app/profile/a", "max_posts": 0}, "max_posts"),
    ({"url": "https://bsky.app/profile/a", "max_posts": 101}, "max_posts"),
    ({"url": "https://bsky.app/profile/a", "max_posts": "5"}, "max_posts"),
    ({"url": "https://bsky.app/profile/a", "max_posts": 1.5}, "max_posts"),
    ({"url": "https://bsky.app/profile/a", "max_comments": -1}, "max_comments"),
    ({"url": "https://bsky.app/profile/a", "max_comments": 201}, "max_comments"),
])
def test_validation_failed(client, body, field):
    r = client.post("/scrape", json=body, headers=AUTH)
    assert r.status_code == 422
    err = r.json()["error"]
    assert err["code"] == "validation_failed" and field in err["details"]["fields"]


def test_unsupported_url(client):
    r = scrape(client, "https://example.com/some/article")
    assert r.status_code == 422 and r.json()["error"]["code"] == "unsupported_url"


def test_facebook_personal_profile(client):
    r = scrape(client, "https://www.facebook.com/profile.php?id=100000000000001")
    assert r.status_code == 422
    e = r.json()["error"]
    assert e["code"] == "unsupported_account_type" and e["details"]["account_type"] == "personal_profile"


@pytest.mark.parametrize("url,platform", [
    ("https://www.instagram.com/alice", "instagram"), ("https://threads.net/@alice", "threads"),
    ("https://www.facebook.com/examplenews", "facebook"), ("https://x.com/alice", "twitter")])
def test_platform_module_not_installed(client, url, platform):
    r = scrape(client, url)
    assert r.status_code == 503
    e = r.json()["error"]
    assert e["code"] == "platform_not_configured" and e["details"]["platform"] == platform


def test_unknown_route_uses_envelope(client):
    r = client.get("/nope")
    assert r.status_code == 404 and r.json()["error"]["code"] == "invalid_request"


class _Boom:
    NAME = "boom"

    def recognize(self, u):
        from app.platforms.base import Recognized
        return Recognized("bluesky", "profile", {"actor": "x"}) if u.host == "boom.test" else None

    async def scrape(self, request):
        raise RuntimeError("secret-stack-detail " + TOKEN)


def test_unhandled_exception_is_500_without_leaks(client, monkeypatch, caplog):
    monkeypatch.setattr(registry, "MODULES", [_Boom()])
    with caplog.at_level(logging.INFO):
        r = scrape(client, "https://boom.test/x")
    assert r.status_code == 500
    assert r.json()["error"]["code"] == "internal_error"
    assert "Traceback" not in r.text and "secret-stack-detail" not in r.text and TOKEN not in r.text


class _Slow(_Boom):
    async def scrape(self, request):
        await asyncio.sleep(5)


def test_timeout_is_504():
    app = create_app(Settings(token=TOKEN, upstream_timeout_s=0.05))
    registry_modules = registry.MODULES
    registry.MODULES = [_Slow()]
    try:
        r = TestClient(app).post("/scrape", json={"url": "https://boom.test/x"}, headers=AUTH)
    finally:
        registry.MODULES = registry_modules
    assert r.status_code == 504
    assert r.json()["error"]["code"] == "upstream_timeout" and r.json()["error"]["details"]["platform"] == "bluesky"


def test_startup_requires_token():
    with pytest.raises(SystemExit, match="SCRAPER_TOKEN"):
        load_settings({})
    with pytest.raises(SystemExit, match="SCRAPER_UPSTREAM_TIMEOUT_SECONDS"):
        load_settings({"SCRAPER_TOKEN": "x", "SCRAPER_UPSTREAM_TIMEOUT_SECONDS": "abc"})
    s = load_settings({"SCRAPER_TOKEN": "x"})
    assert s.upstream_timeout_s == 30


def test_log_redaction(monkeypatch):
    monkeypatch.setenv("TWITTER_AUTH_TOKEN", "super-secret-cookie-value")
    out = redact("headers {'Authorization': 'Bearer abc.def'} Authorization: Bearer xyz cookie super-secret-cookie-value")
    assert "abc.def" not in out and "xyz" not in out and "super-secret" not in out


def test_log_filter_redacts_records_and_keeps_clean_ones(monkeypatch):
    from app.logging_setup import RedactFilter
    monkeypatch.setenv("SCRAPER_TOKEN", "tok-123456789")
    f = RedactFilter()
    bad = logging.LogRecord("x", logging.ERROR, "f", 1, "failed with %s", ("Authorization: Bearer tok-123456789",), None)
    f.filter(bad)
    assert "tok-123456789" not in bad.getMessage()
    ok = logging.LogRecord("x", logging.INFO, "f", 1, '%s - "%s"', ("1.2.3.4", "GET /"), None)
    f.filter(ok)
    assert ok.args == ("1.2.3.4", "GET /")
    try:
        raise RuntimeError("boom tok-123456789")
    except RuntimeError:
        import sys
        rec = logging.LogRecord("x", logging.ERROR, "f", 1, "crashed", (), sys.exc_info())
    f.filter(rec)
    assert "tok-123456789" not in rec.getMessage() and "RuntimeError" in rec.getMessage()
