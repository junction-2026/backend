import json
import pathlib

import pytest
from fastapi.testclient import TestClient

from app.config import Settings
from app.main import create_app

FIXTURES = pathlib.Path(__file__).parent / "fixtures"
TOKEN = "test-token-123456"
AUTH = {"Authorization": f"Bearer {TOKEN}"}


def fx(name: str):
    return json.loads((FIXTURES / name).read_text())


@pytest.fixture
def client():
    # raise_server_exceptions=False: a 500 must come back as the envelope, as in production
    return TestClient(create_app(Settings(token=TOKEN, upstream_timeout_s=5)), raise_server_exceptions=False)


def scrape(client, url, **extra):
    return client.post("/scrape", json={"url": url, **extra}, headers=AUTH)


@pytest.fixture(autouse=True)
def violations(monkeypatch):
    """repo-ci must never start chromium or reach X: tests that need those modules install their own fakes.

    The core turns an exception inside a module into a 500 envelope, so the guard also records the attempt and the
    test fails at teardown even when it never looks at the status."""
    from app.platforms import facebook, x

    hits: list[str] = []

    def no_browser():
        hits.append("browser")
        raise AssertionError("a test tried to start a real browser")

    class NoX:
        def __getattr__(self, name):
            hits.append("x")
            raise AssertionError("a test tried to reach X through the real twscrape API")

    monkeypatch.setattr(facebook, "async_playwright", no_browser)
    monkeypatch.setattr(x, "_api", NoX())
    yield hits
    assert not hits, f"unfaked platform access: {hits}"
