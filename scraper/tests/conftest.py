import json
import pathlib
import socket

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
def no_network(monkeypatch):
    """Tests run offline: any real connection attempt fails the test instead of reaching a platform."""
    def refuse(self, *a, **kw):
        raise RuntimeError("tests must not open network connections")
    monkeypatch.setattr(socket.socket, "connect", refuse)
