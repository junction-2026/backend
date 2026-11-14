import httpx
import pytest
import respx

from .conftest import fx, scrape

H = "https://mastodon.example"
LOOKUP = f"{H}/api/v1/accounts/lookup"
STATUSES = f"{H}/api/v1/accounts/1001/statuses"


@pytest.fixture
def mock():
    with respx.mock(assert_all_called=False) as m:
        yield m


def _profile_routes(mock):
    mock.get(LOOKUP, params={"acct": "alice"}).respond(json=fx("mastodon/account_alice.json"))
    mock.get(STATUSES).respond(json=fx("mastodon/statuses_page1.json"))


def test_profile_scrape(client, mock):
    _profile_routes(mock)
    r = scrape(client, f"{H}/@alice", max_posts=2)
    assert r.status_code == 200
    assert r.headers["content-type"].startswith("application/json")
    b = r.json()
    assert (b["platform"], b["url_kind"], b["username"]) == ("mastodon", "profile", "alice@mastodon.example")
    assert b["display_name"] == "Alice" and b["bio"] == "Birds & books.\nOpinions are mine.\nShe/her"
    assert (b["followers"], b["following"], b["post_count"], b["verified"]) == (321, 12, 1500, None)
    assert b["profile_url"] == f"{H}/@alice"
    assert b["scraped_at"].endswith("Z") and isinstance(b["latency_ms"], int)
    assert [p["id"] for p in b["posts"]] == ["5003", "5002"]  # newest first, capped
    assert b["posts"][0] == {"id": "5003", "text": "Status 5003", "created_at": "2026-11-14T09:30:15Z",
                             "url": f"{H}/@alice/5003", "likes": 5, "replies": 3, "reposts": 2}
    assert b["post"] is None and b["comments"] == [] and b["warnings"] == []
    # first page asks for only what is needed and no boosts
    assert mock.calls[-1].request.url.params["limit"] == "2"
    assert mock.calls[-1].request.url.params["exclude_reblogs"] == "true"


def test_defaults_and_unknown_member(client, mock):
    _profile_routes(mock)
    r = scrape(client, f"{H}/@alice", lang="en")
    assert r.status_code == 200 and len(r.json()["posts"]) == 3
    assert mock.calls[-1].request.url.params["limit"] == "20"


def test_link_header_pagination(client, mock):
    mock.get(LOOKUP).respond(json=fx("mastodon/account_alice.json"))
    page2 = f"{STATUSES}?max_id=5001&limit=40"
    mock.get(STATUSES, params={"limit": "4"}).respond(
        json=fx("mastodon/statuses_page1.json"), headers={"Link": f'<{page2}>; rel="next", <{STATUSES}?min_id=5003>; rel="prev"'})
    mock.get(STATUSES, params={"max_id": "5001"}).respond(json=fx("mastodon/statuses_page2.json"))
    r = scrape(client, f"{H}/@alice", max_posts=4)
    assert [p["id"] for p in r.json()["posts"]] == ["5003", "5002", "5001", "5000"]  # stops at max_posts


def test_pagination_never_leaves_the_instance(client, mock):
    mock.get(LOOKUP).respond(json=fx("mastodon/account_alice.json"))
    evil = mock.get("https://evil.example/steal").respond(json=[])
    mock.get(STATUSES).respond(json=fx("mastodon/statuses_page1.json"),
                               headers={"Link": '<https://evil.example/steal>; rel="next"'})
    r = scrape(client, f"{H}/@alice")
    assert r.status_code == 200 and len(r.json()["posts"]) == 3 and not evil.called


def test_history_truncated_warning(client, mock):
    mock.get(LOOKUP).respond(json=fx("mastodon/account_alice.json"))
    mock.get(STATUSES, params={"limit": "20"}).respond(
        json=fx("mastodon/statuses_page1.json"), headers={"Link": f'<{STATUSES}?max_id=5001>; rel="next"'})
    mock.get(STATUSES, params={"max_id": "5001"}).respond(503)
    b = scrape(client, f"{H}/@alice").json()
    assert len(b["posts"]) == 3 and b["warnings"][0]["code"] == "history_truncated"


def test_history_unavailable_warning(client, mock):
    mock.get(LOOKUP).respond(json=fx("mastodon/account_alice.json"))
    mock.get(STATUSES).respond(403, json={"error": "nope"})
    r = scrape(client, f"{H}/@alice")
    assert r.status_code == 200
    assert r.json()["posts"] == [] and r.json()["warnings"][0]["code"] == "history_unavailable"


def test_post_scrape(client, mock):
    sid = "112233445566778899"
    mock.get(f"{H}/api/v1/statuses/{sid}").respond(json=fx(f"mastodon/status_{sid}.json"))
    mock.get(f"{H}/api/v1/statuses/{sid}/context").respond(json=fx("mastodon/context.json"))
    mock.get(STATUSES).respond(json=fx("mastodon/statuses_page1.json"))
    r = scrape(client, f"{H}/@alice/{sid}")
    assert r.status_code == 200
    b = r.json()
    assert (b["url_kind"], b["username"]) == ("post", "alice@mastodon.example")
    assert b["post"]["id"] == sid and b["post"]["text"] == "Hello & welcome\nBye"
    assert [c["id"] for c in b["comments"]] == ["6001", "6002", "6003"]  # oldest first
    assert b["comments"][0]["author"] == {"username": "bob@mastodon.example", "display_name": "Bob"}
    assert b["comments"][1]["author"] == {"username": "carol@other.example", "display_name": None}
    assert b["comments"][1]["in_reply_to_id"] == "6001" and b["comments"][0]["in_reply_to_id"] == sid
    assert len(b["posts"]) == 3


def test_max_comments_caps_and_zero_skips_context(client, mock):
    sid = "112233445566778899"
    mock.get(f"{H}/api/v1/statuses/{sid}").respond(json=fx(f"mastodon/status_{sid}.json"))
    ctx = mock.get(f"{H}/api/v1/statuses/{sid}/context").respond(json=fx("mastodon/context.json"))
    mock.get(STATUSES).respond(json=[])
    assert [c["id"] for c in scrape(client, f"{H}/users/alice/statuses/{sid}", max_comments=2).json()["comments"]] == ["6001", "6002"]
    ctx.reset()
    b = scrape(client, f"{H}/@alice/{sid}", max_comments=0).json()
    assert b["comments"] == [] and b["warnings"] == [] and not ctx.called


def test_comments_unavailable_warning(client, mock):
    sid = "112233445566778899"
    mock.get(f"{H}/api/v1/statuses/{sid}").respond(json=fx(f"mastodon/status_{sid}.json"))
    mock.get(f"{H}/api/v1/statuses/{sid}/context").respond(502)
    mock.get(STATUSES).respond(json=[])
    r = scrape(client, f"{H}/@alice/{sid}")
    assert r.status_code == 200 and r.json()["comments"] == []
    assert r.json()["warnings"][0]["code"] == "comments_unavailable" and r.json()["post"]["id"] == sid


def test_missing_counters_are_null(client, mock):
    acct = fx("mastodon/account_alice.json")
    del acct["followers_count"]
    mock.get(LOOKUP).respond(json=acct)
    mock.get(STATUSES).respond(json=[{"id": "1", "content": "", "account": acct}])
    b = scrape(client, f"{H}/@alice").json()
    assert b["followers"] is None
    assert b["posts"][0]["text"] == "" and b["posts"][0]["likes"] is None and b["posts"][0]["created_at"] is None


@pytest.mark.parametrize("response,status,code", [
    (httpx.Response(404, json={"error": "Record not found"}), 404, "account_not_found"),
    (httpx.Response(410, json={"error": "gone"}), 404, "account_not_found"),
    (httpx.Response(401, json={"error": "login required"}), 403, "account_private"),
    (httpx.Response(403, json={"error": "private"}), 403, "account_private"),
    (httpx.Response(429, headers={"Retry-After": "42"}), 429, "rate_limited"),
    (httpx.Response(500, text="boom"), 502, "upstream_error"),
    (httpx.Response(200, json=[1, 2]), 502, "upstream_error"),
    (httpx.Response(404, text="<html>Not Found</html>"), 422, "unsupported_url"),
    (httpx.Response(200, text="<html>hi</html>"), 422, "unsupported_url"),
    (httpx.Response(301, headers={"Location": "https://x.example/"}), 422, "unsupported_url"),
])
def test_profile_error_mapping(client, mock, response, status, code):
    mock.get(LOOKUP).mock(return_value=response)
    r = scrape(client, f"{H}/@alice")
    assert r.status_code == status
    e = r.json()["error"]
    assert e["code"] == code and e["details"]["platform"] == "mastodon"
    assert "Traceback" not in r.text
    if code == "rate_limited":
        assert r.headers["retry-after"] == "42" and e["details"]["retry_after_s"] == 42


def test_post_not_found(client, mock):
    mock.get(f"{H}/api/v1/statuses/1").respond(404, json={"error": "Record not found"})
    r = scrape(client, f"{H}/@alice/1")
    assert r.status_code == 404 and r.json()["error"]["code"] == "post_not_found"


def test_upstream_timeout_and_connect_error(client, mock):
    mock.get(LOOKUP).mock(side_effect=httpx.ReadTimeout("slow"))
    r = scrape(client, f"{H}/@alice")
    assert r.status_code == 504 and r.json()["error"]["code"] == "upstream_timeout"
    mock.get(LOOKUP).mock(side_effect=httpx.ConnectError("refused"))
    r = scrape(client, f"{H}/@alice")
    assert r.status_code == 502 and r.json()["error"]["code"] == "upstream_error"


def test_malformed_account_is_upstream_error_not_500(client, mock):
    mock.get(LOOKUP).respond(json={"unexpected": True})
    assert scrape(client, f"{H}/@alice").status_code == 502
