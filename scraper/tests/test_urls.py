import pytest

from app.errors import ScrapeError
from app.registry import recognize

# (url, platform, url_kind) -- every scenario of the shared URL requirements, offline
RECOGNIZED = [
    # Bluesky and Mastodon URLs
    ("https://bsky.app/profile/alice.bsky.social", "bluesky", "profile"),
    ("https://bsky.app/profile/did:plc:abc123", "bluesky", "profile"),
    ("https://bsky.app/profile/alice.bsky.social/post/3k44deefqdk2g", "bluesky", "post"),
    ("https://mastodon.social/@alice", "mastodon", "profile"),
    ("https://mastodon.social/users/alice", "mastodon", "profile"),
    ("https://mastodon.social/@alice/112233445566778899", "mastodon", "post"),
    ("https://mastodon.social/users/alice/statuses/112233445566778899", "mastodon", "post"),
    ("https://fosstodon.org/@alice@other.example/123", "mastodon", "post"),
    # Instagram and Threads URLs
    ("https://www.instagram.com/alice", "instagram", "profile"),
    ("https://www.instagram.com/alice/", "instagram", "profile"),
    ("https://instagram.com/p/C1a2b3c4d5", "instagram", "post"),
    ("https://www.instagram.com/reel/C1a2b3c4d5/", "instagram", "post"),
    ("https://www.threads.net/@alice", "threads", "profile"),
    ("https://threads.com/@alice", "threads", "profile"),
    ("https://www.threads.net/@alice/post/C1a2b3", "threads", "post"),
    ("https://threads.com/@alice/post/C1a2b3", "threads", "post"),
    # Facebook and X URLs
    ("https://www.facebook.com/examplenews", "facebook", "profile"),
    ("https://m.facebook.com/examplenews/posts/1234567890", "facebook", "post"),
    ("https://twitter.com/alice", "twitter", "profile"),
    ("https://x.com/alice", "twitter", "profile"),
    ("https://x.com/alice/status/1790000000000000000", "twitter", "post"),
    ("https://twitter.com/alice/status/1790000000000000000", "twitter", "post"),
    # URL normalization: query, fragment, www./m./mobile. prefixes
    ("https://mobile.x.com/alice/status/1790000000000000000?s=20", "twitter", "post"),
    ("https://www.bsky.app/profile/alice.bsky.social?ref=x#top", "bluesky", "profile"),
    ("http://m.instagram.com/alice?igsh=abc", "instagram", "profile"),
    ("https://MASTODON.social/@alice?foo=1#bar", "mastodon", "profile"),
    ("https://social.example.xn--p1ai/@alice", "mastodon", "profile"),
    ("https://my-instance.example.co.uk/@alice", "mastodon", "profile"),
]

UNSUPPORTED = [
    "https://example.com/some/article",
    "https://example.com/",
    "https://x.com/explore",
    "https://x.com/i/status/1",
    "https://x.com/i/lists/1",
    "https://x.com/alice/followers",
    "https://www.instagram.com/explore/tags/cats",
    "https://www.instagram.com/explore/",
    "https://www.facebook.com/groups/12345",
    "https://bsky.app/",
    "https://bsky.app/search?q=x",
    "https://threads.net/search",
    "https://127.0.0.1/@alice",          # IP literals are never contacted
    "https://localhost/@alice",
    "https://0x7f.0.0.1/@alice",         # hex, octal and short IPv4 spellings
    "https://0x7f.1/@alice",
    "https://0177.0.0.1/@alice",
    "https://2130706433/@alice",
    "https://[::1]/@alice",
    "https://mastodon.social/about",
]


@pytest.mark.parametrize("url,platform,kind", RECOGNIZED)
def test_recognized(url, platform, kind):
    rec = recognize(url)
    assert (rec.platform, rec.url_kind) == (platform, kind)


@pytest.mark.parametrize("url", UNSUPPORTED)
def test_unsupported(url):
    with pytest.raises(ScrapeError) as ei:
        recognize(url)
    assert ei.value.code == "unsupported_url" and ei.value.status == 422


def test_ids():
    assert recognize("https://bsky.app/profile/a.b/post/3k4").ids == {"actor": "a.b", "rkey": "3k4"}
    assert recognize("https://mastodon.social/@alice/1").ids == {"host": "mastodon.social", "user": "alice", "status_id": "1"}
    assert recognize("https://x.com/alice/status/9").ids == {"username": "alice", "status_id": "9"}


def test_facebook_personal_profile():
    with pytest.raises(ScrapeError) as ei:
        recognize("https://www.facebook.com/profile.php?id=100000000000001")
    assert (ei.value.code, ei.value.status) == ("unsupported_account_type", 422)
    assert ei.value.details == {"platform": "facebook", "account_type": "personal_profile"}
