import httpx
import pytest
import respx

from .conftest import fx, scrape

API = "https://public.api.bsky.app/xrpc"
PROFILE, FEED, THREAD = f"{API}/app.bsky.actor.getProfile", f"{API}/app.bsky.feed.getAuthorFeed", f"{API}/app.bsky.feed.getPostThread"
ALICE = "https://bsky.app/profile/alice.bsky.social"


@pytest.fixture
def mock():
    with respx.mock(assert_all_called=False) as m:
        yield m


def _feed_routes(mock):
    mock.get(FEED, params={"actor": "did:plc:alice1234567890abcdefghij"}).mock(side_effect=lambda req: httpx.Response(
        200, json=fx("bluesky/author_feed_page2.json" if req.url.params.get("cursor") == "page2" else "bluesky/author_feed_page1.json")))


def test_profile_scrape(client, mock):
    mock.get(PROFILE).respond(json=fx("bluesky/profile.json"))
    _feed_routes(mock)
    r = scrape(client, ALICE, max_posts=5)
    assert r.status_code == 200
    b = r.json()
    assert (b["platform"], b["url_kind"], b["username"]) == ("bluesky", "profile", "alice.bsky.social")
    assert b["display_name"] == "Alice Example" and b["bio"] == "Writes about birds.\nDMs closed."
    assert (b["followers"], b["following"], b["post_count"], b["verified"]) == (120, 80, 40, True)
    assert b["profile_url"] == ALICE
    # 3 on page 1 (the repost is skipped), 2 of page 2; newest first as the AppView returns them
    assert [p["text"] for p in b["posts"]] == [f"Post number {n}" for n in (6, 5, 4, 3, 2)]
    assert b["posts"][0] == {"id": "at://did:plc:alice1234567890abcdefghij/app.bsky.feed.post/3k06aaaaaaaaaa",
                             "text": "Post number 6", "created_at": "2026-11-16T10:00:00Z",
                             "url": f"{ALICE}/post/3k06aaaaaaaaaa", "likes": 3, "replies": 6, "reposts": 1}
    assert b["post"] is None and b["comments"] == [] and b["warnings"] == []
    assert mock.calls[0].request.url.params["actor"] == "alice.bsky.social"  # no auth header sent
    assert "authorization" not in mock.calls[0].request.headers


def test_verified_unknown_is_null(client, mock):
    p = fx("bluesky/profile.json")
    del p["verification"]
    mock.get(PROFILE).respond(json=p)
    mock.get(FEED).respond(json={"feed": []})
    assert scrape(client, ALICE).json()["verified"] is None


def test_post_scrape(client, mock):
    mock.get(PROFILE).respond(json=fx("bluesky/profile.json"))
    _feed_routes(mock)
    t = mock.get(THREAD).respond(json=fx("bluesky/thread.json"))
    r = scrape(client, f"{ALICE}/post/3k07aaaaaaaaaa")
    assert r.status_code == 200
    b = r.json()
    assert b["url_kind"] == "post" and b["post"]["text"] == "Is anyone else seeing this?"
    assert t.calls[0].request.url.params["uri"] == "at://did:plc:alice1234567890abcdefghij/app.bsky.feed.post/3k07aaaaaaaaaa"
    # oldest first, nested replies flattened, notFoundPost skipped
    assert [c["text"] for c in b["comments"]] == ["Not for me", "Yes, here too", "Thanks for confirming"]
    first = b["comments"][1]
    assert first["author"] == {"username": "bob.example.com", "display_name": None}
    assert first["in_reply_to_id"] == b["post"]["id"]
    assert b["comments"][2]["in_reply_to_id"] == first["id"]
    assert len(b["posts"]) == 6  # page 1 (repost skipped) + page 2


def test_max_comments(client, mock):
    mock.get(PROFILE).respond(json=fx("bluesky/profile.json"))
    mock.get(FEED).respond(json={"feed": []})
    mock.get(THREAD).respond(json=fx("bluesky/thread.json"))
    assert len(scrape(client, f"{ALICE}/post/3k07aaaaaaaaaa", max_comments=1).json()["comments"]) == 1
    assert scrape(client, f"{ALICE}/post/3k07aaaaaaaaaa", max_comments=0).json()["comments"] == []


def test_history_unavailable_is_a_warning(client, mock):
    mock.get(PROFILE).respond(json=fx("bluesky/profile.json"))
    mock.get(FEED).respond(500)
    r = scrape(client, ALICE)
    assert r.status_code == 200 and r.json()["posts"] == []
    assert r.json()["warnings"][0]["code"] == "history_unavailable"


def test_replies_missing_is_comments_unavailable(client, mock):
    t = fx("bluesky/thread.json")
    del t["thread"]["replies"]
    mock.get(PROFILE).respond(json=fx("bluesky/profile.json"))
    mock.get(FEED).respond(json={"feed": []})
    mock.get(THREAD).respond(json=t)
    b = scrape(client, f"{ALICE}/post/3k07aaaaaaaaaa").json()
    assert b["comments"] == [] and b["warnings"][0]["code"] == "comments_unavailable"


@pytest.mark.parametrize("response,status,code", [
    (httpx.Response(400, json=fx("bluesky/error_profile_not_found.json")), 404, "account_not_found"),
    (httpx.Response(401, json={"error": "AuthMissing"}), 502, "upstream_error"),
    (httpx.Response(429, headers={"ratelimit-reset": "1", "retry-after": "30"}, json={"error": "RateLimitExceeded"}), 429, "rate_limited"),
    (httpx.Response(500, json={"error": "InternalServerError"}), 502, "upstream_error"),
    (httpx.Response(502, text="bad gateway"), 502, "upstream_error"),
    (httpx.Response(200, json={"handle": "a"}), 502, "upstream_error"),  # does not parse as a profile
])
def test_profile_error_mapping(client, mock, response, status, code):
    mock.get(PROFILE).mock(return_value=response)
    r = scrape(client, ALICE)
    assert r.status_code == status
    e = r.json()["error"]
    assert e["code"] == code and e["details"]["platform"] == "bluesky"
    if code == "rate_limited":
        assert r.headers["retry-after"] == "30"


def test_timeout_and_network_error(client, mock):
    mock.get(PROFILE).mock(side_effect=httpx.ReadTimeout("slow"))
    r = scrape(client, ALICE)
    assert r.status_code == 504 and r.json()["error"]["code"] == "upstream_timeout"
    mock.get(PROFILE).mock(side_effect=httpx.ConnectError("refused"))
    assert scrape(client, ALICE).status_code == 502


def test_private_account(client, mock):
    mock.get(PROFILE).respond(json=fx("bluesky/profile_private.json"))
    r = scrape(client, ALICE)
    assert r.status_code == 403 and r.json()["error"]["code"] == "account_private"


def test_post_not_found(client, mock):
    mock.get(PROFILE).respond(json=fx("bluesky/profile.json"))
    mock.get(FEED).respond(json={"feed": []})
    mock.get(THREAD).respond(400, json=fx("bluesky/error_post_not_found.json"))
    r = scrape(client, f"{ALICE}/post/3k07aaaaaaaaaa")
    assert r.status_code == 404 and r.json()["error"]["code"] == "post_not_found"
    mock.get(THREAD).respond(json=fx("bluesky/thread_blocked.json"))
    assert scrape(client, f"{ALICE}/post/3k07aaaaaaaaaa").json()["error"]["code"] == "post_not_found"
