"""Facebook module with a fake Playwright: recorded GraphQL documents are replayed, no browser is started."""
import asyncio
import json
from types import SimpleNamespace

import pytest
from fastapi.testclient import TestClient
from playwright.async_api import Error as PlaywrightError
from playwright.async_api import TimeoutError as PlaywrightTimeout

from app.config import Settings
from app.main import create_app
from app.platforms import facebook

from .conftest import AUTH, TOKEN, fx, scrape


class Fake:
    """Controls one fake Playwright session. `batches` are emitted on goto, then one more per scroll."""

    def __init__(self, batches=(), status=200, body="", goto_exc=None, goto_sleep=0):
        self.batches, self.status, self.body = list(batches), status, body
        self.goto_exc, self.goto_sleep = goto_exc, goto_sleep
        self.launch_args = self.goto_url = None
        self.closed = self.entered = self.exited = False
        self.wheels = 0
        self.cb = None

    def emit(self):
        if self.batches:
            docs = self.batches.pop(0)
            text = "\n".join(json.dumps(d) for d in docs)  # one JSON document per line, like batched answers
            self.cb(SimpleNamespace(url="https://www.facebook.com/api/graphql/", status=200, text=_const(text)))
        self.cb(SimpleNamespace(url="https://static.xx.fbcdn.net/app.js", status=200, text=_const("not json")))

    def playwright(self):
        fake = self

        class Page:
            mouse = SimpleNamespace(wheel=None)

            def __init__(s):
                async def wheel(dx, dy):
                    fake.wheels += 1
                    fake.emit()
                s.mouse = SimpleNamespace(wheel=wheel)

            def on(s, event, cb):
                assert event == "response"
                fake.cb = cb

            async def goto(s, url, **kw):
                fake.goto_url = url
                if fake.goto_sleep:
                    await asyncio.sleep(fake.goto_sleep)
                if fake.goto_exc:
                    raise fake.goto_exc
                fake.emit()
                return SimpleNamespace(status=fake.status)

            async def wait_for_timeout(s, ms):
                pass

            async def inner_text(s, selector, timeout=None):
                return fake.body

        class Ctx:
            async def new_page(s):
                return Page()

        class Browser:
            async def new_context(s, **kw):
                return Ctx()

            async def close(s):
                fake.closed = True

        class Chromium:
            async def launch(s, **kw):
                fake.launch_args = kw
                return Browser()

        class CM:
            async def __aenter__(s):
                fake.entered = True
                return SimpleNamespace(chromium=Chromium())

            async def __aexit__(s, *a):
                fake.exited = True

        return CM()


def _const(v):
    async def f():
        return v
    return f


@pytest.fixture
def fake(monkeypatch):
    def install(**kw):
        f = Fake(**kw)
        monkeypatch.setattr(facebook, "async_playwright", f.playwright)
        return f
    return install


PAGE = "https://www.facebook.com/examplenews"


def test_page_profile_with_scrolled_history(client, fake):
    f = fake(batches=[fx("facebook/page_graphql_1.json"), fx("facebook/page_graphql_2.json")])
    r = scrape(client, PAGE, max_posts=3)
    assert r.status_code == 200
    b = r.json()
    assert (b["platform"], b["url_kind"], b["username"], b["display_name"]) == ("facebook", "profile", "examplenews", "Example News")
    assert b["bio"] == "Local news & weather" and b["followers"] == 45210 and b["verified"] is True
    assert b["following"] is None and b["profile_url"] == PAGE
    assert [p["id"] for p in b["posts"]] == ["9003", "9002", "9001"]  # newest first; the other owner's story is dropped
    assert b["posts"][0] == {"id": "9003", "text": "Newest story", "created_at": "2026-11-14T09:30:15Z",
                             "url": f"{PAGE}/posts/9003", "likes": 40, "replies": 7, "reposts": 3}
    assert b["posts"][1]["replies"] is None and b["post"] is None and b["comments"] == [] and b["warnings"] == []
    assert f.wheels == 1  # scrolled once to load the second batch, then had enough
    assert "--no-sandbox" in f.launch_args["args"] and f.launch_args["headless"] is True
    assert f.goto_url == PAGE and f.closed and f.exited


def test_profile_without_enough_posts_warns(client, fake):
    fake(batches=[fx("facebook/page_graphql_1.json")])
    b = scrape(client, PAGE, max_posts=10).json()
    assert [p["id"] for p in b["posts"]] == ["9003", "9002"]
    assert [w["code"] for w in b["warnings"]] == ["history_truncated"]


def test_page_post_with_comments(client, fake):
    f = fake(batches=[fx("facebook/post_graphql.json")])
    r = scrape(client, "https://m.facebook.com/examplenews/posts/9003?ref=x", max_comments=5)
    assert r.status_code == 200
    b = r.json()
    assert (b["url_kind"], b["username"], b["post"]["id"], b["post"]["text"]) == ("post", "examplenews", "9003", "Newest story")
    assert b["followers"] == 45210 and b["verified"] is None and b["bio"] is None
    assert [c["id"] for c in b["comments"]] == ["c1", "c2"]  # oldest first
    c1, c2 = b["comments"]
    assert c1["author"] == {"username": "300", "display_name": "Eli"} and c1["in_reply_to_id"] == "9003" and c1["likes"] == 2
    assert c2["author"]["username"] == "dana.k" and c2["in_reply_to_id"] == "c1"
    assert [p["id"] for p in b["posts"]] == ["9003"]
    assert f.goto_url == f"{PAGE}/posts/9003"  # canonical URL on facebook.com, query dropped


def test_post_comments_capped_or_skipped(client, fake):
    fake(batches=[fx("facebook/post_graphql.json")])
    assert len(scrape(client, f"{PAGE}/posts/9003", max_comments=1).json()["comments"]) == 1
    fake(batches=[fx("facebook/post_graphql.json")])
    b = scrape(client, f"{PAGE}/posts/9003", max_comments=0).json()
    assert b["comments"] == [] and b["warnings"] == []


def test_post_without_comments_warns(client, fake):
    docs = [d for d in fx("facebook/post_graphql.json") if "comments" not in d["data"]]
    fake(batches=[docs])
    b = scrape(client, f"{PAGE}/posts/9003").json()
    assert b["post"]["id"] == "9003" and [w["code"] for w in b["warnings"]] == ["comments_unavailable"]


def test_missing_follower_count_is_partial_parse(client, fake):
    docs = fx("facebook/page_graphql_1.json")
    del docs[0]["data"]["page"]["followers_count"]
    fake(batches=[docs])
    b = scrape(client, PAGE, max_posts=2).json()
    assert b["followers"] is None and "partial_parse" in [w["code"] for w in b["warnings"]]


def test_personal_profile_by_vanity_url(client, fake):
    f = fake(batches=[fx("facebook/personal_graphql.json")])
    r = scrape(client, "https://www.facebook.com/jane.doe")
    assert r.status_code == 422
    e = r.json()["error"]
    assert e["code"] == "unsupported_account_type" and e["details"] == {"platform": "facebook", "account_type": "personal_profile"}
    assert f.closed


def test_commenter_user_nodes_are_not_the_profile(client, fake):
    # the post page above has User nodes (commenters); the Page node must win
    fake(batches=[fx("facebook/post_graphql.json")])
    assert scrape(client, f"{PAGE}/posts/9003").status_code == 200


def test_unavailable_text_wins_over_unrelated_graphql(client, fake):
    other = [{"data": {"viewer": {"__typename": "User", "id": "1"}}}]  # a real unavailable page still fires GraphQL
    fake(batches=[other], body="This content isn't available right now")
    assert scrape(client, PAGE).json()["error"]["code"] == "account_not_found"
    fake(batches=[other], body="This content isn't available right now")
    assert scrape(client, f"{PAGE}/posts/1").json()["error"]["code"] == "post_not_found"


def test_page_node_without_id_is_not_a_500(client, fake):
    docs = fx("facebook/page_graphql_1.json")
    del docs[0]["data"]["page"]["id"]
    fake(batches=[docs])
    r = scrape(client, PAGE, max_posts=2)
    assert r.status_code == 200 and len(r.json()["posts"]) == 2


def test_unfaked_browser_fails_the_test(client, violations):
    assert scrape(client, PAGE).status_code == 500  # the core hides the assertion behind the envelope...
    assert violations == ["browser"]                # ...so the guard records it and would fail the test
    violations.clear()


@pytest.mark.parametrize("url,status,body,code", [
    (PAGE, 404, "", "account_not_found"),
    (PAGE, 200, "This content isn't available right now", "account_not_found"),
    (f"{PAGE}/posts/1", 200, "This content isn't available right now", "post_not_found"),
    (PAGE, 200, "Log in to continue", "upstream_error"),
])
def test_unavailable_pages(client, fake, url, status, body, code):
    f = fake(status=status, body=body)
    r = scrape(client, url)
    assert r.json()["error"]["code"] == code and r.json()["error"]["details"]["platform"] == "facebook"
    assert f.closed


def test_post_missing_from_page_is_not_found(client, fake):
    fake(batches=[fx("facebook/post_graphql.json")])  # the page loads, but names another post
    r = scrape(client, f"{PAGE}/posts/424242")
    assert r.status_code == 404 and r.json()["error"]["code"] == "post_not_found"


@pytest.mark.parametrize("exc,status,code", [
    (PlaywrightTimeout("Timeout 5000ms exceeded"), 504, "upstream_timeout"),
    (PlaywrightError("net::ERR_CONNECTION_RESET"), 502, "upstream_error"),
])
def test_browser_failures_close_the_browser(client, fake, exc, status, code):
    f = fake(goto_exc=exc)
    r = scrape(client, PAGE)
    assert r.status_code == status and r.json()["error"]["code"] == code
    assert f.closed and f.exited


def test_upstream_timeout_cancels_and_closes_browser(fake):
    f = fake(goto_sleep=5)
    c = TestClient(create_app(Settings(token=TOKEN, upstream_timeout_s=0.2)), raise_server_exceptions=False)
    r = c.post("/scrape", json={"url": PAGE}, headers=AUTH)
    assert r.status_code == 504 and r.json()["error"]["code"] == "upstream_timeout"
    assert f.closed and f.exited
