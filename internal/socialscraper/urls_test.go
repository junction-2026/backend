package socialscraper

import (
	"strings"
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
		// hosts of unsupported networks get no special case: a Mastodon-shaped path is a Mastodon candidate
		{"https://www.threads.net/@zuck", "mastodon", "profile"},
		{"https://example.com/@zuck", "mastodon", "profile"},
		// query string, fragment and the www./m./mobile. prefixes are ignored
		{"https://www.bsky.app/profile/alice.bsky.social?ref=x#top", "bluesky", "profile"},
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

func TestWebFallback(t *testing.T) {
	for _, u := range []string{
		"https://www.dw.com/en/some-article/a-12345678",
		"https://example.com/some/article",
		"https://example.com/",
		"https://x.com/alice",
		"https://x.com/alice/status/1790000000000000000",
		"https://twitter.com/alice",
		"https://www.instagram.com/reel/C1a2b3c4d5/",
		"https://www.threads.com/@zuck/post/abc", // not a Mastodon status shape
		"https://www.facebook.com/examplenews",
		"https://www.facebook.com/profile.php?id=100000000000001",
		"https://bsky.app/",
		"https://bsky.app/search?q=x",
		"https://bsky.app/@alice", // bsky.app is never a Mastodon candidate
		"https://www.bsky.app/users/alice",
		"https://mastodon.social/about",
		"https://mastodon.social/%2540alice",
		"https://8.8.8.8/page", // public IP literals are scraped
		"http://[2606:4700::1111]/",
	} {
		t.Run(u, func(t *testing.T) {
			got, err := Recognize(u)
			require.Nil(t, err)
			assert.Equal(t, "web", got.Platform)
			assert.Equal(t, "page", got.URLKind)
		})
	}
}

func TestUnsupported(t *testing.T) {
	for _, u := range []string{
		"ftp://example.com/a",
		"/relative/path",
		"example.com/a",
		"https://exa mple.com/a",
		"https:///nohost",
		"https://user:secret@news.example.org/a",
		"https://user@news.example.org/a",
		"https://news.example.org/" + strings.Repeat("a", 2001),
		"http://localhost/article",
		"http://LOCALHOST./article",
		"http://app.localhost/",
		"http://127.0.0.1/admin",
		"http://127.1.2.3/",
		"http://10.0.0.5/x",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data",
		"http://0.0.0.0/",
		"http://0.1.2.3/",
		"http://[::1]/",
		"http://[::]/",
		"http://[fc00::1]/",
		"http://[fe80::1]/",
		"http://[::ffff:127.0.0.1]/",
		"http://[::ffff:10.0.0.1]/",
		"https://0x7f.0.0.1/@alice", // legacy IPv4 spellings
		"https://0x7f.1/@alice",
		"https://0177.0.0.1/@alice",
		"https://2130706433/@alice",
	} {
		t.Run(u, func(t *testing.T) {
			_, err := Recognize(u)
			require.NotNil(t, err)
			assert.Equal(t, "unsupported_url", err.Code)
			assert.Equal(t, 422, err.Status)
			assert.NotContains(t, err.Message, "secret")
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestMaxLengthAccepted(t *testing.T) {
	u := "https://news.example.org/" + strings.Repeat("a", 2000-len("https://news.example.org/"))
	require.Len(t, u, 2000)
	got, err := Recognize(u)
	require.Nil(t, err)
	assert.Equal(t, "web", got.Platform)
}

func TestIDs(t *testing.T) {
	for url, want := range map[string]map[string]string{
		"https://bsky.app/profile/a.b/post/3k4": {"actor": "a.b", "rkey": "3k4"},
		"https://mastodon.social/@alice/1":      {"host": "mastodon.social", "user": "alice", "status_id": "1"},
		"https://mastodon.social/users/alice":   {"host": "mastodon.social", "user": "alice"},
		"https://www.threads.net/@zuck":         {"host": "threads.net", "user": "zuck"},
		"https://example.com/@zuck":             {"host": "example.com", "user": "zuck"},
	} {
		got, err := Recognize(url)
		require.Nil(t, err, url)
		assert.Equal(t, want, got.IDs, url)
	}
}
