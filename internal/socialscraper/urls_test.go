package socialscraper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecognized(t *testing.T) {
	for _, c := range []struct{ url, platform, kind string }{
		{"https://bsky.app/profile/alice.bsky.social", "bluesky", "profile"},
		{"https://bsky.app/profile/did:plc:abc123", "bluesky", "profile"},
		{"https://bsky.app/profile/alice.bsky.social/post/3k44deefqdk2g", "bluesky", "post"},
		{"https://mastodon.social/@alice", "mastodon", "profile"},
		{"https://mastodon.social/users/alice", "mastodon", "profile"},
		{"https://mastodon.social/@alice/112233445566778899", "mastodon", "post"},
		{"https://mastodon.social/users/alice/statuses/112233445566778899", "mastodon", "post"},
		{"https://fosstodon.org/@alice@other.example/123", "mastodon", "post"},
		{"https://www.instagram.com/alice", "instagram", "profile"},
		{"https://www.instagram.com/alice/", "instagram", "profile"},
		{"https://instagram.com/p/C1a2b3c4d5", "instagram", "post"},
		{"https://www.instagram.com/reel/C1a2b3c4d5/", "instagram", "post"},
		{"https://www.threads.net/@alice", "threads", "profile"},
		{"https://threads.com/@alice", "threads", "profile"},
		{"https://www.threads.net/@alice/post/C1a2b3", "threads", "post"},
		{"https://threads.com/@alice/post/C1a2b3", "threads", "post"},
		{"https://www.facebook.com/examplenews", "facebook", "profile"},
		{"https://m.facebook.com/examplenews/posts/1234567890", "facebook", "post"},
		{"https://twitter.com/alice", "twitter", "profile"},
		{"https://x.com/alice", "twitter", "profile"},
		{"https://x.com/alice/status/1790000000000000000", "twitter", "post"},
		{"https://twitter.com/alice/status/1790000000000000000", "twitter", "post"},
		// query string, fragment and the www./m./mobile. prefixes are ignored
		{"https://mobile.x.com/alice/status/1790000000000000000?s=20", "twitter", "post"},
		{"https://www.bsky.app/profile/alice.bsky.social?ref=x#top", "bluesky", "profile"},
		{"http://m.instagram.com/alice?igsh=abc", "instagram", "profile"},
		{"https://MASTODON.social/@alice?foo=1#bar", "mastodon", "profile"},
		{"https://m.mastodon.social/@alice", "mastodon", "profile"},
		{"https://social.example.xn--p1ai/@alice", "mastodon", "profile"},
		{"https://my-instance.example.co.uk/@alice", "mastodon", "profile"},
		{"https://mastodon.social:8443/@alice", "mastodon", "profile"},
	} {
		t.Run(c.url, func(t *testing.T) {
			got, err := Recognize(c.url)
			require.Nil(t, err)
			assert.Equal(t, c.platform, got.Platform)
			assert.Equal(t, c.kind, got.URLKind)
		})
	}
}

func TestUnsupported(t *testing.T) {
	for _, u := range []string{
		"https://example.com/some/article",
		"https://example.com/",
		"https://x.com/explore",
		"https://x.com/i/status/1",
		"https://x.com/i/lists/1",
		"https://x.com/alice/followers",
		"https://x.com/alice/status/abc",
		"https://www.instagram.com/explore/tags/cats",
		"https://www.instagram.com/explore/",
		"https://www.facebook.com/groups/12345",
		"https://www.facebook.com/",
		"https://bsky.app/",
		"https://bsky.app/search?q=x",
		"https://threads.net/search",
		"https://mastodon.social/about",
		"https://mastodon.social/",
		"https://127.0.0.1/@alice", // IP literals and dotless hosts are never contacted
		"https://localhost/@alice",
		"https://0x7f.0.0.1/@alice",
		"https://0x7f.1/@alice",
		"https://0177.0.0.1/@alice",
		"https://2130706433/@alice",
		"https://[::1]/@alice",
		"https://mastodon.social/%2540alice",
	} {
		t.Run(u, func(t *testing.T) {
			_, err := Recognize(u)
			require.NotNil(t, err)
			assert.Equal(t, "unsupported_url", err.Code)
			assert.Equal(t, 422, err.Status)
		})
	}
}

func TestFacebookPersonalProfile(t *testing.T) {
	_, err := Recognize("https://www.facebook.com/profile.php?id=100000000000001")
	require.NotNil(t, err)
	assert.Equal(t, "unsupported_account_type", err.Code)
	assert.Equal(t, 422, err.Status)
	assert.Equal(t, map[string]any{"platform": "facebook", "account_type": "personal_profile"}, err.Details)
}

func TestIDs(t *testing.T) {
	for url, want := range map[string]map[string]string{
		"https://bsky.app/profile/a.b/post/3k4":    {"actor": "a.b", "rkey": "3k4"},
		"https://mastodon.social/@alice/1":         {"host": "mastodon.social", "user": "alice", "status_id": "1"},
		"https://mastodon.social/users/alice":      {"host": "mastodon.social", "user": "alice"},
		"https://x.com/alice/status/9":             {"username": "alice", "status_id": "9"},
		"https://www.facebook.com/page/posts/77":   {"page": "page", "post_id": "77"},
		"https://threads.net/@alice/post/C1":       {"username": "alice", "code": "C1"},
		"https://instagram.com/reel/C1a2b3c4d5/?x": {"shortcode": "C1a2b3c4d5"},
	} {
		got, err := Recognize(url)
		require.Nil(t, err, url)
		assert.Equal(t, want, got.IDs, url)
	}
}
