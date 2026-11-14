package bluesky

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/scrapertest"
)

const (
	host  = "public.api.bsky.app"
	did   = "did:plc:alice1234567890abcdefghij"
	alice = "https://bsky.app/profile/alice.bsky.social"
)

// fake is the AppView: one handler per XRPC method, every request recorded.
type fake struct {
	t       *testing.T
	mu      sync.Mutex
	routes  map[string]http.HandlerFunc
	queries []map[string]string
}

func newFake(t *testing.T) (*fake, *Module) {
	f := &fake{t: t, routes: map[string]http.HandlerFunc{}}
	router := scrapertest.New(t).Handle(host, f)
	return f, New(router.Client())
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	q := map[string]string{"method": strings.TrimPrefix(r.URL.Path, "/xrpc/")}
	for k := range r.URL.Query() {
		q[k] = r.URL.Query().Get(k)
	}
	f.queries = append(f.queries, q)
	h := f.routes[q["method"]]
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "" {
		f.t.Errorf("an Authorization header was sent")
	}
	if h == nil {
		f.t.Errorf("unexpected call %s", r.URL)
		w.WriteHeader(500)
		return
	}
	h(w, r)
}

func (f *fake) on(method string, h http.HandlerFunc) { f.routes[method] = h }

func (f *fake) calls(method string) (out []map[string]string) {
	for _, q := range f.queries {
		if q["method"] == method {
			out = append(out, q)
		}
	}
	return out
}

func (f *fake) profile(body []byte) { f.on("app.bsky.actor.getProfile", scrapertest.Body(200, body)) }

func (f *fake) feed() {
	f.on("app.bsky.feed.getAuthorFeed", func(w http.ResponseWriter, r *http.Request) {
		file := "author_feed_page1.json"
		if r.URL.Query().Get("cursor") == "page2" {
			file = "author_feed_page2.json"
		}
		scrapertest.Body(200, scrapertest.Fixture(f.t, file))(w, r)
	})
}

func profileTarget() ss.Target {
	return ss.Target{Platform: "bluesky", URLKind: "profile", IDs: map[string]string{"actor": "alice.bsky.social"}}
}

func postTarget() ss.Target {
	return ss.Target{Platform: "bluesky", URLKind: "post", IDs: map[string]string{"actor": "alice.bsky.social", "rkey": "3k07aaaaaaaaaa"}}
}

func req(posts, comments int) ss.Request { return ss.Request{MaxPosts: posts, MaxComments: comments} }

func mustErr(t *testing.T, err error) *ss.Error {
	t.Helper()
	var se *ss.Error
	require.True(t, errors.As(err, &se), "got %v", err)
	return se
}

func texts(posts []ss.Post) (out []string) {
	for _, p := range posts {
		out = append(out, p.Text)
	}
	return out
}

func TestProfileHistoryWithRepostOverTwoPages(t *testing.T) {
	f, m := newFake(t)
	f.profile(scrapertest.Fixture(t, "profile.json"))
	f.feed()
	res, err := m.Scrape(context.Background(), profileTarget(), req(5, 50))
	require.NoError(t, err)

	assert.Equal(t, "bluesky", res.Platform)
	assert.Equal(t, "profile", res.URLKind)
	assert.Equal(t, "alice.bsky.social", res.Username)
	assert.Equal(t, "Alice Example", *res.DisplayName)
	assert.Equal(t, "Writes about birds.\nDMs closed.", *res.Bio)
	assert.Equal(t, alice, *res.ProfileURL)
	assert.Equal(t, 120, *res.Followers)
	assert.Equal(t, 80, *res.Following)
	assert.Equal(t, 40, *res.PostCount)
	assert.Equal(t, true, *res.Verified)
	// page 1 has 3 own posts and a repost, page 2 more: 5 own posts, newest first, none the repost
	assert.Equal(t, []string{"Post number 6", "Post number 5", "Post number 4", "Post number 3", "Post number 2"}, texts(res.Posts))
	assert.Equal(t, ss.Post{
		ID: "at://" + did + "/app.bsky.feed.post/3k06aaaaaaaaaa", Text: "Post number 6", CreatedAt: ss.Ptr("2026-11-16T10:00:00Z"),
		URL: ss.Ptr(alice + "/post/3k06aaaaaaaaaa"), Likes: ss.Ptr(3), Replies: ss.Ptr(6), Reposts: ss.Ptr(1),
	}, res.Posts[0])
	assert.Nil(t, res.Post)
	assert.Empty(t, res.Comments)
	assert.Empty(t, res.Warnings)

	assert.Equal(t, "alice.bsky.social", f.calls("app.bsky.actor.getProfile")[0]["actor"])
	feed := f.calls("app.bsky.feed.getAuthorFeed")
	require.Len(t, feed, 2)
	assert.Equal(t, did, feed[0]["actor"])
	assert.Equal(t, "page2", feed[1]["cursor"])
	assert.Equal(t, "posts_no_replies", feed[0]["filter"])
	assert.Equal(t, "100", feed[0]["limit"])
}

func TestHistoryStopsAtMaxPosts(t *testing.T) {
	f, m := newFake(t)
	f.profile(scrapertest.Fixture(t, "profile.json"))
	f.feed()
	res, err := m.Scrape(context.Background(), profileTarget(), req(2, 50))
	require.NoError(t, err)
	assert.Len(t, res.Posts, 2)
	assert.Len(t, f.calls("app.bsky.feed.getAuthorFeed"), 1) // page 1 holds enough
}

func TestVerifiedUnknownIsNull(t *testing.T) {
	f, m := newFake(t)
	p := fixtureMap(t, "profile.json")
	delete(p, "verification")
	f.profile(mustJSON(p))
	f.on("app.bsky.feed.getAuthorFeed", scrapertest.Body(200, []byte(`{"feed": []}`)))
	res, err := m.Scrape(context.Background(), profileTarget(), req(20, 50))
	require.NoError(t, err)
	assert.Nil(t, res.Verified)
}

func TestPostThread(t *testing.T) {
	f, m := newFake(t)
	f.profile(scrapertest.Fixture(t, "profile.json"))
	f.feed()
	f.on("app.bsky.feed.getPostThread", scrapertest.Body(200, scrapertest.Fixture(t, "thread.json")))
	res, err := m.Scrape(context.Background(), postTarget(), req(20, 50))
	require.NoError(t, err)

	assert.Equal(t, "post", res.URLKind)
	assert.Equal(t, "Is anyone else seeing this?", res.Post.Text)
	assert.Equal(t, "at://"+did+"/app.bsky.feed.post/3k07aaaaaaaaaa", f.calls("app.bsky.feed.getPostThread")[0]["uri"])
	// oldest first, nested replies flattened, the deleted reply skipped
	var got []string
	for _, c := range res.Comments {
		got = append(got, c.Text)
	}
	assert.Equal(t, []string{"Not for me", "Yes, here too", "Thanks for confirming"}, got)
	second := res.Comments[1]
	assert.Equal(t, ss.Author{Username: "bob.example.com"}, second.Author)
	assert.Equal(t, res.Post.ID, *second.InReplyToID)
	assert.Equal(t, second.ID, *res.Comments[2].InReplyToID)
	assert.Len(t, res.Posts, 6) // the history of both pages, repost skipped
	assert.Empty(t, res.Warnings)
}

func TestMaxComments(t *testing.T) {
	f, m := newFake(t)
	f.profile(scrapertest.Fixture(t, "profile.json"))
	f.on("app.bsky.feed.getAuthorFeed", scrapertest.Body(200, []byte(`{"feed": []}`)))
	f.on("app.bsky.feed.getPostThread", scrapertest.Body(200, scrapertest.Fixture(t, "thread.json")))
	res, err := m.Scrape(context.Background(), postTarget(), req(20, 1))
	require.NoError(t, err)
	assert.Len(t, res.Comments, 1)
	res, err = m.Scrape(context.Background(), postTarget(), req(20, 0))
	require.NoError(t, err)
	assert.Empty(t, res.Comments)
	assert.Empty(t, res.Warnings)
}

func TestFeedFailureIsHistoryUnavailableWarning(t *testing.T) {
	f, m := newFake(t)
	f.profile(scrapertest.Fixture(t, "profile.json"))
	f.on("app.bsky.feed.getAuthorFeed", scrapertest.Body(500, []byte(`{"error":"InternalServerError"}`)))
	res, err := m.Scrape(context.Background(), profileTarget(), req(20, 50))
	require.NoError(t, err)
	assert.Empty(t, res.Posts)
	require.Len(t, res.Warnings, 1)
	assert.Equal(t, "history_unavailable", res.Warnings[0].Code)
}

func TestSecondPageFailureIsHistoryTruncatedWarning(t *testing.T) {
	f, m := newFake(t)
	f.profile(scrapertest.Fixture(t, "profile.json"))
	f.on("app.bsky.feed.getAuthorFeed", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "page2" {
			scrapertest.Body(502, []byte("bad gateway"), "Content-Type", "text/plain")(w, r)
			return
		}
		scrapertest.Body(200, scrapertest.Fixture(t, "author_feed_page1.json"))(w, r)
	})
	res, err := m.Scrape(context.Background(), profileTarget(), req(20, 50))
	require.NoError(t, err)
	assert.Len(t, res.Posts, 3)
	require.Len(t, res.Warnings, 1)
	assert.Equal(t, "history_truncated", res.Warnings[0].Code)
}

func TestMissingRepliesIsCommentsUnavailableWarning(t *testing.T) {
	f, m := newFake(t)
	thread := fixtureMap(t, "thread.json")
	delete(thread["thread"].(map[string]any), "replies")
	f.profile(scrapertest.Fixture(t, "profile.json"))
	f.on("app.bsky.feed.getAuthorFeed", scrapertest.Body(200, []byte(`{"feed": []}`)))
	f.on("app.bsky.feed.getPostThread", scrapertest.Body(200, mustJSON(thread)))
	res, err := m.Scrape(context.Background(), postTarget(), req(20, 50))
	require.NoError(t, err)
	assert.Empty(t, res.Comments)
	require.Len(t, res.Warnings, 1)
	assert.Equal(t, "comments_unavailable", res.Warnings[0].Code)
}

func TestProfileErrorMapping(t *testing.T) {
	for name, c := range map[string]struct {
		h      http.HandlerFunc
		status int
		code   string
	}{
		"not found":     {scrapertest.Body(400, scrapertest.Fixture(t, "error_profile_not_found.json")), 404, "account_not_found"},
		"401":           {scrapertest.Body(401, []byte(`{"error":"AuthMissing"}`)), 502, "upstream_error"},
		"429":           {scrapertest.Body(429, []byte(`{"error":"RateLimitExceeded"}`), "Retry-After", "30", "Ratelimit-Reset", "1"), 429, "rate_limited"},
		"500":           {scrapertest.Body(500, []byte(`{"error":"InternalServerError"}`)), 502, "upstream_error"},
		"html 502":      {scrapertest.Body(502, []byte("bad gateway"), "Content-Type", "text/html"), 502, "upstream_error"},
		"not a profile": {scrapertest.Body(200, []byte(`{"handle":"a"}`)), 502, "upstream_error"},
		"wrong type":    {scrapertest.Body(200, []byte(`{"did":"d","handle":"a","followersCount":"many"}`)), 502, "upstream_error"},
		"not json":      {scrapertest.Body(200, []byte(`<html>`)), 502, "upstream_error"},
	} {
		t.Run(name, func(t *testing.T) {
			f, m := newFake(t)
			f.on("app.bsky.actor.getProfile", c.h)
			_, err := m.Scrape(context.Background(), profileTarget(), req(20, 50))
			se := mustErr(t, err)
			assert.Equal(t, c.status, se.Status)
			assert.Equal(t, c.code, se.Code)
			assert.Equal(t, "bluesky", se.Details["platform"])
			if c.code == "rate_limited" {
				assert.Equal(t, "30", se.Header.Get("Retry-After"))
			}
		})
	}
}

func TestTimeoutAndNetworkError(t *testing.T) {
	slow := scrapertest.New(t).Handle(host, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := New(slow.Client()).Scrape(ctx, profileTarget(), req(20, 50))
	assert.Equal(t, "upstream_timeout", mustErr(t, err).Code)
	assert.Equal(t, 504, mustErr(t, err).Status)

	down := scrapertest.New(t).Fail(host, errors.New("connection refused"))
	_, err = New(down.Client()).Scrape(context.Background(), profileTarget(), req(20, 50))
	assert.Equal(t, 502, mustErr(t, err).Status)
}

func TestPrivateAccount(t *testing.T) {
	f, m := newFake(t)
	f.profile(scrapertest.Fixture(t, "profile_private.json"))
	_, err := m.Scrape(context.Background(), profileTarget(), req(20, 50))
	se := mustErr(t, err)
	assert.Equal(t, 403, se.Status)
	assert.Equal(t, "account_private", se.Code)
}

func TestPostNotFound(t *testing.T) {
	f, m := newFake(t)
	f.profile(scrapertest.Fixture(t, "profile.json"))
	f.on("app.bsky.feed.getAuthorFeed", scrapertest.Body(200, []byte(`{"feed": []}`)))
	f.on("app.bsky.feed.getPostThread", scrapertest.Body(400, scrapertest.Fixture(t, "error_post_not_found.json")))
	_, err := m.Scrape(context.Background(), postTarget(), req(20, 50))
	assert.Equal(t, "post_not_found", mustErr(t, err).Code)
	assert.Equal(t, 404, mustErr(t, err).Status)

	f.on("app.bsky.feed.getPostThread", scrapertest.Body(200, scrapertest.Fixture(t, "thread_blocked.json")))
	_, err = m.Scrape(context.Background(), postTarget(), req(20, 50))
	assert.Equal(t, "post_not_found", mustErr(t, err).Code)
}

func TestBioIsTrimmed(t *testing.T) {
	f, m := newFake(t)
	p := fixtureMap(t, "profile.json")
	p["description"] = "  hi\n"
	f.profile(mustJSON(p))
	f.on("app.bsky.feed.getAuthorFeed", scrapertest.Body(200, []byte(`{"feed": []}`)))
	res, err := m.Scrape(context.Background(), profileTarget(), req(20, 50))
	require.NoError(t, err)
	assert.Equal(t, "hi", *res.Bio)
}

func fixtureMap(t *testing.T, name string) map[string]any {
	var m map[string]any
	require.NoError(t, json.Unmarshal(scrapertest.Fixture(t, name), &m))
	return m
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
