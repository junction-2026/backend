"""Threads module against recorded SSR pages; httpx is mocked, nothing reaches the network."""
import pytest
import respx

from .conftest import FIXTURES, scrape

BASE = "https://www.threads.com"


def html(name):
    return (FIXTURES / "threads" / name).read_text()


@pytest.fixture
def mock():
    with respx.mock(assert_all_called=False) as m:
        yield m


def test_profile(client, mock):
    route = mock.get(f"{BASE}/@alice").respond(text=html("profile.html"))
    r = scrape(client, "https://www.threads.net/@alice", max_posts=2)
    assert r.status_code == 200
    b = r.json()
    assert (b["platform"], b["url_kind"], b["username"], b["display_name"]) == ("threads", "profile", "alice", "Alice Example")
    assert b["bio"] == "Birds & books." and b["profile_url"] == f"{BASE}/@alice"
    assert (b["followers"], b["following"], b["post_count"], b["verified"]) == (4321, None, None, True)
    assert [p["id"] for p in b["posts"]] == ["7003", "7002"]  # newest first, capped
    assert b["posts"][0] == {"id": "7003", "text": "Third thread", "created_at": "2025-11-14T08:50:15Z",
                             "url": f"{BASE}/@alice/post/T3", "likes": 30, "replies": 4, "reposts": 2}
    assert b["post"] is None and b["comments"] == [] and b["warnings"] == []
    assert "Mozilla/5.0" in route.calls[0].request.headers["user-agent"]


def test_post(client, mock):
    mock.get(f"{BASE}/@alice/post/T2").respond(text=html("post.html"))
    mock.get(f"{BASE}/@alice").respond(text=html("profile.html"))
    b = scrape(client, "https://threads.com/@alice/post/T2", max_comments=1).json()
    assert (b["url_kind"], b["username"], b["post"]["id"], b["post"]["text"]) == ("post", "alice", "7002", "Second thread")
    assert [c["id"] for c in b["comments"]] == ["8001"] and b["comments"][0]["author"]["username"] == "carol"
    assert b["comments"][0]["in_reply_to_id"] is None  # not stated by the page
    assert b["followers"] == 4321 and b["bio"] == "Birds & books."  # completed from the profile page
    assert [p["id"] for p in b["posts"]] == ["7003", "7002", "7001"] and b["warnings"] == []
    both = scrape(client, "https://threads.com/@alice/post/T2").json()
    assert [(c["id"], c["likes"]) for c in both["comments"]] == [("8001", 2), ("8002", None)]  # null likes stay null


def test_post_without_profile_page_still_succeeds(client, mock):
    mock.get(f"{BASE}/@alice/post/T2").respond(text=html("post.html"))
    mock.get(f"{BASE}/@alice").respond(429)
    b = scrape(client, "https://threads.com/@alice/post/T2").json()
    assert b["post"]["id"] == "7002" and b["followers"] is None and b["posts"] == []
    assert {w["code"] for w in b["warnings"]} == {"history_unavailable", "partial_parse"}


def test_missing_or_renamed_keys_give_nulls_and_warnings(client, mock):
    mock.get(f"{BASE}/@alice").respond(text=html("profile_renamed.html"))
    r = scrape(client, "https://threads.net/@alice")
    assert r.status_code == 200
    b = r.json()
    assert (b["followers"], b["bio"], b["verified"], b["following"]) == (None, None, None, None)
    assert [w["code"] for w in b["warnings"]] == ["partial_parse", "partial_parse"]
    assert "follower count" in b["warnings"][1]["message"] and "1 post(s)" in b["warnings"][0]["message"]
    posts = {p["id"]: p for p in b["posts"]}
    assert set(posts) == {"7003", "7001"}  # the post without `code` is skipped, never a 500
    assert (posts["7003"]["likes"], posts["7003"]["replies"], posts["7003"]["reposts"]) == (None, None, None)


def test_private_account(client, mock):
    mock.get(f"{BASE}/@alice").respond(text=html("profile_private.html"))
    r = scrape(client, "https://www.threads.net/@alice")
    assert r.status_code == 403 and r.json()["error"]["code"] == "account_private"


def test_unrecognizable_page_is_upstream_error(client, mock):
    mock.get(f"{BASE}/@alice").respond(text=html("profile_unrecognizable.html"))
    r = scrape(client, "https://www.threads.net/@alice")
    assert r.status_code == 502
    assert r.json()["error"]["code"] == "upstream_error" and r.json()["error"]["details"]["platform"] == "threads"


@pytest.mark.parametrize("body", ["", "<html>no scripts</html>", '<script type="application/json">{broken</script>',
                                  '<script type="application/json">[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[1]]]]]]]]]]]]]]]]]]]]]]]]]]]]]</script>'])
def test_garbage_pages_never_500(client, mock, body):
    mock.get(f"{BASE}/@alice").respond(text=body)
    assert scrape(client, "https://www.threads.net/@alice").status_code == 502


def test_post_page_without_the_post(client, mock):
    mock.get(f"{BASE}/@alice/post/NOPE").respond(text=html("profile.html"))
    assert scrape(client, "https://www.threads.net/@alice/post/NOPE").status_code == 502


def test_not_found(client, mock):
    mock.get(f"{BASE}/@nobody").respond(404)
    mock.get(f"{BASE}/@alice/post/GONE").respond(404)
    assert scrape(client, "https://www.threads.net/@nobody").json()["error"]["code"] == "account_not_found"
    assert scrape(client, "https://www.threads.net/@alice/post/GONE").json()["error"]["code"] == "post_not_found"


def test_rate_limited_with_retry_info(client, mock):
    mock.get(f"{BASE}/@alice").respond(429, headers={"Retry-After": "120"})
    r = scrape(client, "https://www.threads.net/@alice")
    assert r.status_code == 429 and r.headers["Retry-After"] == "120"
    assert r.json()["error"]["details"]["retry_after_s"] == 120


def test_rate_limited_without_retry_info(client, mock):
    mock.get(f"{BASE}/@alice").respond(429)
    r = scrape(client, "https://www.threads.net/@alice")
    assert r.status_code == 429 and "Retry-After" not in r.headers


def test_upstream_failures(client, mock):
    import httpx
    mock.get(f"{BASE}/@alice").respond(500)
    assert scrape(client, "https://www.threads.net/@alice").json()["error"]["code"] == "upstream_error"
    mock.get(f"{BASE}/@alice").mock(side_effect=httpx.ReadTimeout("slow"))
    assert scrape(client, "https://www.threads.net/@alice").json()["error"]["code"] == "upstream_timeout"
    mock.get(f"{BASE}/@alice").mock(side_effect=httpx.ConnectError("down"))
    assert scrape(client, "https://www.threads.net/@alice").json()["error"]["code"] == "upstream_error"
    mock.get(f"{BASE}/@alice").respond(302, headers={"Location": "https://evil.example/"})
    assert scrape(client, "https://www.threads.net/@alice").json()["error"]["code"] == "upstream_error"
