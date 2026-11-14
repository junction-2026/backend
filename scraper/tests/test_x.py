"""X module with a fake twscrape API built from recorded fixtures; the real pool is only touched for its sqlite file."""
import logging
from datetime import datetime
from types import SimpleNamespace

import pytest
from twscrape import API
from twscrape.accounts_pool import NoAccountError
from twscrape.models import Media, Tweet, User

from app.platforms import x

from .conftest import fx, scrape

SENTINEL_AUTH, SENTINEL_CT0 = "SENTINEL-auth-6f1c9d2b", "SENTINEL-ct0-8a7e3b5d"
POST = 1790000000000000000


def _users():
    out = {}
    for name, d in fx("x/users.json").items():
        d = {**d, "created": datetime.fromisoformat(d["created"])}
        out[name] = User(id_str=str(d["id"]), url=f"https://x.com/{name}", favouritesCount=0, listedCount=0, mediaCount=0,
                         location="", profileImageUrl="https://example.invalid/a.jpg", **d)
    return out


def _tweets():
    users, out = _users(), []
    for d in fx("x/tweets.json"):
        u = users[d["user"]]
        t = Tweet(id=d["id"], id_str=str(d["id"]), url=f"https://x.com/{u.username}/status/{d['id']}",
                  date=datetime.fromisoformat(d["date"]), user=u, lang="en", rawContent=d["rawContent"],
                  replyCount=d["replyCount"], retweetCount=d["retweetCount"], likeCount=d["likeCount"], quoteCount=0,
                  bookmarkedCount=0, conversationId=POST, conversationIdStr=str(POST), hashtags=[], cashtags=[],
                  mentionedUsers=[], links=[], media=Media())
        if d.get("retweet"):
            t.retweetedTweet = out[0]
        if d.get("inReplyTo"):
            t.inReplyToTweetId, t.inReplyToTweetIdStr = d["inReplyTo"], str(d["inReplyTo"])
        out.append(t)
    return users, out


class FakeAPI:
    def __init__(self, fail=None, active=1):
        self.users, self.tweets = _tweets()
        self.fail, self.calls = fail, []
        self.pool = SimpleNamespace(stats=self._stats)
        self._active = active

    async def _stats(self):
        return {"active": self._active}

    def _check(self, name):
        self.calls.append(name)
        if self.fail and name in self.fail:
            raise self.fail[name]

    async def user_by_login(self, login):
        self._check("user_by_login")
        return self.users.get(login)

    async def tweet_details(self, twid):
        self._check("tweet_details")
        return next((t for t in self.tweets if t.id == twid), None)

    async def user_tweets(self, uid, limit=-1):
        self._check("user_tweets")
        for t in self.tweets:
            if t.user.id == uid and not t.inReplyToTweetId:
                yield t

    async def tweet_replies(self, twid, limit=-1):
        self._check("tweet_replies")
        for t in self.tweets:
            if t.inReplyToTweetId == twid:
                yield t


@pytest.fixture
def api(monkeypatch):
    monkeypatch.setenv("TWITTER_AUTH_TOKEN", SENTINEL_AUTH)
    monkeypatch.setenv("TWITTER_CT0", SENTINEL_CT0)
    a = FakeAPI()
    monkeypatch.setattr(x, "_api", a)
    return a


def test_unfaked_x_api_fails_the_test(client, monkeypatch, violations):
    monkeypatch.setenv("TWITTER_AUTH_TOKEN", SENTINEL_AUTH)
    monkeypatch.setenv("TWITTER_CT0", SENTINEL_CT0)
    assert scrape(client, "https://x.com/alice").status_code == 500
    assert violations == ["x"]
    violations.clear()


def test_profile(client, api):
    r = scrape(client, "https://x.com/alice", max_posts=2)
    assert r.status_code == 200
    b = r.json()
    assert (b["platform"], b["url_kind"], b["username"], b["display_name"]) == ("twitter", "profile", "alice", "Alice A.")
    assert b["bio"] == "Birds & books.\nOpinions are mine."
    assert (b["followers"], b["following"], b["post_count"], b["verified"]) == (321, 12, 1500, True)
    assert b["profile_url"] == "https://x.com/alice"
    assert [p["id"] for p in b["posts"]] == ["1790000000000000000", "1789000000000000000"]  # no retweet, newest first, capped
    assert b["posts"][0] == {"id": "1790000000000000000", "text": "Hello & welcome <3", "created_at": "2026-11-14T09:30:15Z",
                             "url": "https://x.com/alice/status/1790000000000000000", "likes": 5, "replies": 2, "reposts": 1}
    assert b["post"] is None and b["comments"] == [] and b["warnings"] == []


def test_post_with_replies_via_mobile_url(client, api):
    r = scrape(client, "https://mobile.x.com/alice/status/1790000000000000000?s=20")
    assert r.status_code == 200
    b = r.json()
    assert (b["platform"], b["url_kind"], b["post"]["id"], b["username"]) == ("twitter", "post", "1790000000000000000", "alice")
    assert [c["text"] for c in b["comments"]] == ["First reply", "Second reply"]  # oldest first although served newest first
    assert b["comments"][0]["author"] == {"username": "carol", "display_name": "Carol"}
    assert b["comments"][0]["in_reply_to_id"] == "1790000000000000000" and b["comments"][0]["likes"] == 2
    assert len(b["posts"]) == 3 and b["warnings"] == []
    assert len(scrape(client, "https://x.com/alice/status/1790000000000000000", max_comments=1).json()["comments"]) == 1
    api.calls.clear()
    assert scrape(client, "https://x.com/alice/status/1790000000000000000", max_comments=0).json()["comments"] == []
    assert "tweet_replies" not in api.calls


@pytest.mark.parametrize("url,code", [
    ("https://x.com/nobody", "account_not_found"),
    ("https://x.com/alice/status/42", "post_not_found"),
    ("https://x.com/bob", "account_private"),
])
def test_not_found_and_private(client, api, url, code):
    r = scrape(client, url)
    assert r.json()["error"]["code"] == code and r.json()["error"]["details"]["platform"] == "twitter"


def test_secondary_failures_are_warnings(client, api):
    api.fail = {"user_tweets": RuntimeError("boom"), "tweet_replies": RuntimeError("boom")}
    b = scrape(client, "https://x.com/alice/status/1790000000000000000").json()
    assert b["post"]["id"] == "1790000000000000000" and b["posts"] == [] and b["comments"] == []
    assert sorted(w["code"] for w in b["warnings"]) == ["comments_unavailable", "history_unavailable"]


@pytest.mark.parametrize("exc,status,code", [
    (TimeoutError(), 504, "upstream_timeout"),
    (RuntimeError("curl: 403 " + SENTINEL_CT0), 502, "upstream_error"),
])
def test_primary_failures(client, api, exc, status, code):
    api.fail = {"user_by_login": exc}
    r = scrape(client, "https://x.com/alice")
    assert r.status_code == status and r.json()["error"]["code"] == code


def test_pool_exhausted_is_rate_limited_or_rejected_session(client, api):
    api.fail = {"user_by_login": NoAccountError("none")}
    assert scrape(client, "https://x.com/alice").status_code == 429
    api._active = 0
    r = scrape(client, "https://x.com/alice")
    assert r.status_code == 502 and "cookies" in r.json()["error"]["message"]


@pytest.mark.parametrize("unset", [["TWITTER_AUTH_TOKEN"], ["TWITTER_CT0"], ["TWITTER_AUTH_TOKEN", "TWITTER_CT0"]])
def test_platform_not_configured(client, api, monkeypatch, unset):
    for n in unset:
        monkeypatch.delenv(n)
    r = scrape(client, "https://x.com/alice")
    assert r.status_code == 503
    e = r.json()["error"]
    assert e["code"] == "platform_not_configured" and e["details"]["platform"] == "twitter"
    assert e["details"]["missing_settings"] == unset
    assert SENTINEL_AUTH not in r.text and SENTINEL_CT0 not in r.text


def test_empty_variable_counts_as_unset(client, api, monkeypatch):
    monkeypatch.setenv("TWITTER_CT0", "")
    assert scrape(client, "https://x.com/alice").json()["error"]["details"]["missing_settings"] == ["TWITTER_CT0"]


def test_other_platforms_work_without_x_credentials(client, monkeypatch):
    monkeypatch.delenv("TWITTER_AUTH_TOKEN", raising=False)
    monkeypatch.delenv("TWITTER_CT0", raising=False)
    # Instagram is still a pending module (503 by design) and an unknown host is not X: neither touches the X pool
    assert scrape(client, "https://example.com/x").json()["error"]["code"] == "unsupported_url"
    assert scrape(client, "https://x.com/alice").json()["error"]["code"] == "platform_not_configured"


async def test_pool_is_built_from_the_environment_outside_the_repo(monkeypatch, tmp_path, capfd):
    monkeypatch.setenv("TWITTER_AUTH_TOKEN", SENTINEL_AUTH)
    monkeypatch.setenv("TWITTER_CT0", SENTINEL_CT0)
    monkeypatch.setenv("SCRAPER_X_POOL_DB", str(tmp_path / "pool.db"))
    monkeypatch.setattr(x, "_api", None)
    api = await x._get_api()
    assert isinstance(api, API) and (tmp_path / "pool.db").exists()
    [acc] = await api.pool.get_all()
    assert acc.cookies == {"auth_token": SENTINEL_AUTH, "ct0": SENTINEL_CT0} and acc.active
    assert await x._get_api() is api
    out = capfd.readouterr()
    assert SENTINEL_AUTH not in out.out + out.err  # twscrape's own logging stays quiet


async def test_default_pool_path_is_a_private_temp_dir(monkeypatch):
    import os
    import tempfile
    monkeypatch.setenv("TWITTER_AUTH_TOKEN", SENTINEL_AUTH)
    monkeypatch.setenv("TWITTER_CT0", SENTINEL_CT0)
    monkeypatch.delenv("SCRAPER_X_POOL_DB", raising=False)
    monkeypatch.setattr(x, "_api", None)
    api = await x._get_api()
    db = api.pool._db_file
    assert db.startswith(tempfile.gettempdir()) and oct(os.stat(os.path.dirname(db)).st_mode & 0o777) == "0o700"
    assert not os.path.abspath(db).startswith(os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))


def test_credentials_never_reach_responses_or_logs(client, api, caplog, capfd):
    api.fail = {"user_tweets": RuntimeError("cookie " + SENTINEL_AUTH + " " + SENTINEL_CT0)}
    bodies = []
    with caplog.at_level(logging.DEBUG):
        for url in ("https://x.com/alice", "https://x.com/alice/status/1790000000000000000", "https://x.com/nobody"):
            r = scrape(client, url)
            bodies.append(r.text + str(dict(r.headers)))
        api.fail = {"user_by_login": RuntimeError("cookie " + SENTINEL_AUTH + " " + SENTINEL_CT0)}
        r = scrape(client, "https://x.com/alice")
        bodies.append(r.text + str(dict(r.headers)))
    out = capfd.readouterr()
    everything = "".join(bodies) + caplog.text + out.out + out.err
    assert SENTINEL_AUTH not in everything and SENTINEL_CT0 not in everything
    assert "upstream_error" in bodies[-1]
