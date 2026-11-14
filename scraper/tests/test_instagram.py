"""Instagram module against recorded instaloader responses; the instaloader context never touches the network."""
import copy
import logging
import pickle
import time

import instaloader
import pytest
import requests
from instaloader import exceptions as ex

from app.platforms import instagram

from .conftest import fx, scrape

MEDIA = {c: f"instagram/media_{c}.json" for c in ("CpC333", "CpB222", "CpA111")}


class Net:
    """Stands in for the HTTP methods of InstaloaderContext; counts calls so tests can prove nothing else ran."""

    def __init__(self, page="instagram/page_alice.json", media=None, comments=None, page_error=None, media_errors=None,
                 profile_info=None):
        self.page, self.media, self.comments = page, media or dict(MEDIA), comments
        self.page_error, self.media_errors, self.profile_info = page_error, media_errors or {}, profile_info
        self.calls = []

    def get_page_data(self, path):
        self.calls.append(("page", path))
        if self.page_error:
            raise self.page_error
        return copy.deepcopy(fx(self.page))

    def doc_id_graphql_query(self, doc_id, variables, referer=None, web_api=False):
        code = variables.get("shortcode")
        self.calls.append(("media", code))
        if code in self.media_errors:
            raise self.media_errors[code]
        return copy.deepcopy(fx(self.media[code]))

    def get_json(self, path, params, *a, **kw):  # instaloader's fallback profile lookup (web_profile_info)
        self.calls.append(("json", path))
        if self.profile_info is None:
            raise ex.ConnectionException("no recorded response for " + path)
        return copy.deepcopy(fx(self.profile_info))

    def graphql_query(self, query_hash, variables, referer=None):
        self.calls.append(("graphql", variables.get("shortcode")))
        return copy.deepcopy(fx(self.comments))


@pytest.fixture
def net(monkeypatch):
    def install(**kw):
        n = Net(**kw)
        real = instagram._loader

        def loader(timeout_s):
            L = real(timeout_s)
            for name in ("get_page_data", "doc_id_graphql_query", "graphql_query", "get_json"):
                setattr(L.context, name, getattr(n, name))
            return L
        monkeypatch.setattr(instagram, "_loader", loader)
        return n
    monkeypatch.delenv("INSTAGRAM_SESSION_FILE", raising=False)
    return install


def test_profile(client, net):
    n = net()
    r = scrape(client, "https://www.instagram.com/Alice/", max_posts=2)
    assert r.status_code == 200
    b = r.json()
    assert (b["platform"], b["url_kind"], b["username"], b["display_name"]) == ("instagram", "profile", "alice", "Alice Example")
    assert b["bio"] == "Birds & books.\nOpinions are mine." and b["profile_url"] == "https://www.instagram.com/alice/"
    assert (b["followers"], b["following"], b["post_count"], b["verified"]) == (4321, 87, 3, True)
    assert [p["id"] for p in b["posts"]] == ["CpC333", "CpB222"]  # newest first, capped at max_posts
    assert b["posts"][0] == {"id": "CpC333", "text": "Third post", "created_at": "2025-11-14T08:50:15Z",
                             "url": "https://www.instagram.com/p/CpC333/", "likes": 30, "replies": 2, "reposts": None}
    assert b["post"] is None and b["comments"] == [] and b["warnings"] == []
    assert ("media", "CpA111") not in n.calls  # stops fetching once max_posts is reached


def test_hidden_and_absent_counters_are_null(client, net):
    net()
    posts = {p["id"]: p for p in scrape(client, "https://instagram.com/alice").json()["posts"]}
    assert posts["CpA111"]["text"] == "" and posts["CpA111"]["likes"] is None and posts["CpA111"]["replies"] is None
    assert posts["CpB222"]["likes"] == 20


def test_post_url_anonymous_has_no_comments_warning(client, net):
    n = net()
    r = scrape(client, "https://www.instagram.com/p/CpB222/", max_comments=5, max_posts=3)
    assert r.status_code == 200
    b = r.json()
    assert (b["url_kind"], b["username"], b["post"]["id"], b["post"]["replies"]) == ("post", "alice", "CpB222", 1)
    assert b["comments"] == [] and [w["code"] for w in b["warnings"]] == ["comments_unavailable"]
    assert [p["id"] for p in b["posts"]] == ["CpC333", "CpB222", "CpA111"] and b["followers"] == 4321
    assert ("media", "CpB222") in n.calls


def test_reel_url_and_zero_max_comments_skip_comments(client, net):
    net()
    b = scrape(client, "https://www.instagram.com/reel/CpB222/", max_comments=0).json()
    assert b["post"]["id"] == "CpB222" and b["warnings"] == []


def test_comments_with_a_session(monkeypatch):
    """A logged-in context serves comments; answers become comments of their parent; oldest first, capped."""
    ctx = instaloader.Instaloader(quiet=True).context
    ctx.username = "viewer"
    n = Net(comments="instagram/comments_CpB222.json")
    ctx.graphql_query = n.graphql_query
    ctx.doc_id_graphql_query = n.doc_id_graphql_query
    post = instaloader.Post.from_shortcode(ctx, "CpB222")
    post._node["comment_count"] = 3
    got = instagram._comments(post, 10, time.monotonic() + 5, [])
    assert [(c.id, c.in_reply_to_id, c.author.username) for c in got] == [
        ("9001", None, "erin"), ("9002", None, "carol"), ("9003", "9002", "dave")]
    assert got[0].text == "first parent"  # padded in the fixture
    assert got[0].likes == 5 and got[0].created_at == "2025-11-13T09:10:00Z"
    assert [c.id for c in instagram._comments(post, 2, time.monotonic() + 5, [])] == ["9001", "9002"]


def _session_post():
    ctx = instaloader.Instaloader(quiet=True).context
    ctx.username = "viewer"
    n = Net(comments="instagram/comments_CpB222.json")
    ctx.graphql_query, ctx.doc_id_graphql_query = n.graphql_query, n.doc_id_graphql_query
    post = instaloader.Post.from_shortcode(ctx, "CpB222")
    post._node["comment_count"] = 3
    return post, list(post.get_comments())


def test_comments_time_budget_keeps_what_was_read_and_warns():
    post, comments = _session_post()
    warnings = []
    got = instagram._comments(post, 10, time.monotonic() - 1, warnings)  # budget already used up
    assert [c.id for c in got] == ["9002", "9003"] and [w.code for w in warnings] == ["comments_unavailable"]
    assert "time budget" in warnings[0].message


def test_comments_failure_midway_keeps_what_was_read():
    post, comments = _session_post()

    def broken():
        yield comments[1]  # 9001, no answers
        raise ex.ConnectionException("500 Internal Server Error")
    post.get_comments = broken
    warnings = []
    got = instagram._comments(post, 10, time.monotonic() + 5, warnings)
    assert [c.id for c in got] == ["9001"] and [w.code for w in warnings] == ["comments_unavailable"]


def test_private_account(client, net):
    net(page="instagram/page_private.json")
    r = scrape(client, "https://www.instagram.com/secret_bob")
    assert r.status_code == 403
    assert r.json()["error"]["code"] == "account_private" and r.json()["error"]["details"]["platform"] == "instagram"


def test_missing_account(client, net):
    net(page="instagram/page_not_found.json")
    r = scrape(client, "https://www.instagram.com/nobody_here")
    assert r.status_code == 404 and r.json()["error"]["code"] == "account_not_found"


def test_missing_account_404_from_instagram(client, net):
    net(page_error=ex.QueryReturnedNotFoundException("404 Not Found"))
    assert scrape(client, "https://www.instagram.com/nobody_here").json()["error"]["code"] == "account_not_found"


def test_missing_post(client, net):
    net(media={"CpZ999": "instagram/media_empty.json"})
    r = scrape(client, "https://www.instagram.com/p/CpZ999/")
    assert r.status_code == 404 and r.json()["error"]["code"] == "post_not_found"


def _wrapped_429():
    try:
        try:
            raise ex.TooManyRequestsException("429 Too Many Requests when accessing https://www.instagram.com/alice/")
        except ex.TooManyRequestsException as e:
            raise ex.ConnectionException("JSON Query to graphql: 429 Too Many Requests") from e  # as get_json wraps it
    except ex.ConnectionException as e:
        return e


@pytest.mark.parametrize("error", [_wrapped_429(), ex.LoginRequiredException("Login required"),
                                   ex.ConnectionException("Page request: 429 Too Many Requests")])
def test_throttled_or_login_wall_is_rate_limited(client, net, error):
    net(page_error=error)
    r = scrape(client, "https://www.instagram.com/alice")
    assert r.status_code == 429 and r.json()["error"]["code"] == "rate_limited"
    assert r.json()["error"]["details"] == {"platform": "instagram"}  # instaloader exposes no Retry-After
    assert "Retry-After" not in r.headers


def test_throttled_midway_keeps_the_posts_fetched(client, net):
    net(media_errors={"CpB222": _wrapped_429()})
    b = scrape(client, "https://www.instagram.com/alice").json()
    assert [p["id"] for p in b["posts"]] == ["CpC333"] and b["warnings"][0]["code"] == "history_truncated"


def test_timeout_and_other_failures(client, net):
    try:
        raise ex.ConnectionException("Page request") from requests.exceptions.ReadTimeout("slow")
    except ex.ConnectionException as e:
        net(page_error=e)
    assert scrape(client, "https://www.instagram.com/alice").json()["error"]["code"] == "upstream_timeout"
    net(page_error=ex.ConnectionException("500 Internal Server Error"))
    assert scrape(client, "https://www.instagram.com/alice").json()["error"]["code"] == "upstream_error"


def test_missing_members_are_null_with_warning(client, net):
    net(page="instagram/page_no_counters.json", profile_info="instagram/web_profile_info_no_counters.json")
    b = scrape(client, "https://www.instagram.com/alice").json()
    assert (b["followers"], b["following"], b["post_count"], b["verified"]) == (None, None, None, None)
    assert b["bio"] == "Birds & books.\nOpinions are mine." and b["warnings"][0]["code"] == "partial_parse"


def test_session_file_is_optional_and_never_logged(monkeypatch, tmp_path, caplog):
    secret = "sessionid-SECRET-VALUE"
    f = tmp_path / "session-viewer"
    f.write_bytes(pickle.dumps({"csrftoken": "tok", "sessionid": secret}))
    monkeypatch.setenv("INSTAGRAM_SESSION_FILE", str(f))
    with caplog.at_level(logging.DEBUG):
        loader = instagram._loader(5)
    assert loader.context.username == "viewer" and loader.context.is_logged_in
    bad = tmp_path / "session-broken"
    bad.write_bytes(b"not a pickle")
    monkeypatch.setenv("INSTAGRAM_SESSION_FILE", str(bad))
    with caplog.at_level(logging.DEBUG):
        anon = instagram._loader(5)
    assert not anon.context.is_logged_in
    assert secret not in caplog.text and str(f) not in caplog.text and str(bad) not in caplog.text


def test_loader_never_sleeps_or_retries():
    L = instagram._loader(7)
    assert L.context.sleep is False and L.context.max_connection_attempts == 1 and L.context.request_timeout == 7
    assert L.context._rate_controller.wait_before_query("x") is None
