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
