package threads

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/scrapertest"
)

const host = "www.threads.net"

// site is Threads: handlers by path, every request recorded.
type site struct {
	t      *testing.T
	routes map[string]http.HandlerFunc
	seen   []*http.Request
}

func newSite(t *testing.T) (*site, *Module) {
	s := &site{t: t, routes: map[string]http.HandlerFunc{}}
	return s, New(scrapertest.New(t).Handle(host, s).Handle("www.threads.com", s).Client())
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.seen = append(s.seen, r)
	h := s.routes[r.URL.Path]
	if h == nil {
		s.t.Errorf("unexpected request %s", r.URL)
		w.WriteHeader(500)
		return
	}
	h(w, r)
}

func (s *site) html(path string, status int, fixture string, headers ...string) {
	s.raw(path, status, string(scrapertest.Fixture(s.t, fixture)), headers...)
}

func (s *site) raw(path string, status int, body string, headers ...string) {
	headers = append([]string{"Content-Type", "text/html; charset=utf-8"}, headers...)
	s.routes[path] = scrapertest.Body(status, []byte(body), headers...)
}

func profile(user string) ss.Target {
	return ss.Target{Platform: "threads", URLKind: "profile", IDs: map[string]string{"username": user}}
}

func post(user, code string) ss.Target {
	return ss.Target{Platform: "threads", URLKind: "post", IDs: map[string]string{"username": user, "code": code}}
}

func scrape(m *Module, t ss.Target, posts, comments int) (*ss.Result, error) {
	return m.Scrape(context.Background(), t, ss.Request{MaxPosts: posts, MaxComments: comments})
}

func mustErr(t *testing.T, err error) *ss.Error {
	t.Helper()
	var se *ss.Error
	require.ErrorAs(t, err, &se)
	return se
}

func codes(w []ss.Warning) (out []string) {
	for _, x := range w {
		out = append(out, x.Code)
	}
	return out
}

func ids(ps []ss.Post) (out []string) {
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

func TestProfile(t *testing.T) {
	s, m := newSite(t)
	s.html("/@alice", 200, "profile.html")
	res, err := scrape(m, profile("alice"), 2, 5)
	require.NoError(t, err)
	assert.Equal(t, "threads", res.Platform)
	assert.Equal(t, "profile", res.URLKind)
	assert.Equal(t, "alice", res.Username)
	assert.Equal(t, "Alice Example", *res.DisplayName)
	assert.Equal(t, "Birds & books.", *res.Bio, "trimmed")
	assert.Equal(t, "https://www.threads.net/@alice", *res.ProfileURL)
	assert.Equal(t, 4321, *res.Followers)
	assert.Nil(t, res.Following, "not exposed by Threads")
	assert.Nil(t, res.PostCount)
	assert.True(t, *res.Verified)
	assert.Equal(t, []string{"7003", "7002"}, ids(res.Posts), "newest first, capped")
	assert.Equal(t, ss.Post{ID: "7003", Text: "Third thread", CreatedAt: ss.Ptr("2025-11-14T08:50:15Z"),
		URL: ss.Ptr("https://www.threads.net/@alice/post/T3"), Likes: ss.Ptr(30), Replies: ss.Ptr(4), Reposts: ss.Ptr(2)}, res.Posts[0])
	assert.Nil(t, res.Post)
	assert.Empty(t, res.Comments)
	assert.Empty(t, res.Warnings)
	require.Len(t, s.seen, 1)
	assert.Contains(t, s.seen[0].Header.Get("User-Agent"), "Mozilla/5.0")
}

func TestPostWithRepliesAndAuthorHistory(t *testing.T) {
	s, m := newSite(t)
	s.html("/@alice/post/T2", 200, "post.html")
	s.html("/@alice", 200, "profile.html")
	res, err := scrape(m, post("alice", "T2"), 5, 1)
	require.NoError(t, err)
	assert.Equal(t, "post", res.URLKind)
	assert.Equal(t, "alice", res.Username)
	assert.Equal(t, "7002", res.Post.ID)
	assert.Equal(t, "Second thread", res.Post.Text)
	require.Len(t, res.Comments, 1)
	assert.Equal(t, "8001", res.Comments[0].ID)
	assert.Equal(t, "carol", res.Comments[0].Author.Username)
	assert.Nil(t, res.Comments[0].InReplyToID, "the page does not state it")
	assert.Equal(t, 4321, *res.Followers, "completed from the profile page")
	assert.Equal(t, "Birds & books.", *res.Bio)
	assert.Equal(t, []string{"7003", "7002", "7001"}, ids(res.Posts))
	assert.Empty(t, res.Warnings)

	res, err = scrape(m, post("alice", "T2"), 5, 10)
	require.NoError(t, err)
	require.Len(t, res.Comments, 2)
	assert.Equal(t, 2, *res.Comments[0].Likes)
	assert.Nil(t, res.Comments[1].Likes, "a null like count stays null")
}

func TestThreadsComHost(t *testing.T) {
	s, m := newSite(t)
	s.html("/@alice", 200, "profile.html")
	tg := profile("alice")
	tg.URL = mustURL(t, "https://threads.com/@alice")
	res, err := scrape(m, tg, 1, 0)
	require.NoError(t, err)
	assert.Equal(t, "https://www.threads.com/@alice", *res.ProfileURL)
	assert.Equal(t, "www.threads.com", s.seen[0].URL.Host)
}

func TestPostWithoutAuthorPageStillSucceeds(t *testing.T) {
	s, m := newSite(t)
	s.html("/@alice/post/T2", 200, "post.html")
	s.raw("/@alice", 429, "")
	res, err := scrape(m, post("alice", "T2"), 5, 0)
	require.NoError(t, err)
	assert.Equal(t, "7002", res.Post.ID)
	assert.Nil(t, res.Followers)
	assert.Empty(t, res.Posts)
	assert.ElementsMatch(t, []string{"history_unavailable", "partial_parse"}, codes(res.Warnings))
}

func TestMissingOrRenamedKeysGiveNullsAndWarnings(t *testing.T) {
	s, m := newSite(t)
	s.html("/@alice", 200, "profile_renamed.html")
	res, err := scrape(m, profile("alice"), 10, 0)
	require.NoError(t, err)
	assert.Nil(t, res.Followers)
	assert.Nil(t, res.Bio, "renamed biography key")
	assert.Nil(t, res.Verified)
	assert.Equal(t, []string{"partial_parse", "partial_parse"}, codes(res.Warnings))
	assert.Contains(t, res.Warnings[0].Message, "1 post(s)")
	assert.Contains(t, res.Warnings[1].Message, "follower count")
	byID := map[string]ss.Post{}
	for _, p := range res.Posts {
		byID[p.ID] = p
	}
	assert.Len(t, byID, 2, "the post without a code is skipped")
	assert.Contains(t, byID, "7003")
	assert.Contains(t, byID, "7001")
	assert.Nil(t, byID["7003"].Likes)
	assert.Nil(t, byID["7003"].Replies)
	assert.Nil(t, byID["7003"].Reposts)
}

func TestCodelessPostObjectsWarnOnceOnPostURLs(t *testing.T) {
	s, m := newSite(t)
	s.html("/@alice/post/T2", 200, "post_codeless.html")
	s.html("/@alice", 200, "profile_codeless.html")
	res, err := scrape(m, post("alice", "T2"), 10, 5)
	require.NoError(t, err)
	assert.Equal(t, "7002", res.Post.ID)
	assert.Equal(t, []string{"7003", "7002", "7001"}, ids(res.Posts))
	require.Equal(t, []string{"partial_parse"}, codes(res.Warnings), "one warning for both pages")
	assert.Contains(t, res.Warnings[0].Message, "post(s) could not be parsed")
}

func TestPrivateAccount(t *testing.T) {
	s, m := newSite(t)
	s.html("/@alice", 200, "profile_private.html")
	_, err := scrape(m, profile("alice"), 2, 0)
	e := mustErr(t, err)
	assert.Equal(t, 403, e.Status)
	assert.Equal(t, "account_private", e.Code)
}

func TestUnrecognizableAndGarbagePages(t *testing.T) {
	deep := strings.Repeat("[", 5000) + "1" + strings.Repeat("]", 5000)
	for name, body := range map[string]string{
		"layout changed": string(scrapertest.Fixture(t, "profile_unrecognizable.html")),
		"empty":          "",
		"no scripts":     "<html>no scripts</html>",
		"broken json":    `<script type="application/json">{broken</script>`,
		"deep nesting":   `<script type="application/json">` + deep + `</script>`,
		"other script":   `<script>{"username":"alice","pk":"1"}</script>`,
	} {
		t.Run(name, func(t *testing.T) {
			s, m := newSite(t)
			s.raw("/@alice", 200, body)
			_, err := scrape(m, profile("alice"), 2, 0)
			e := mustErr(t, err)
			assert.Equal(t, 502, e.Status)
			assert.Equal(t, "upstream_error", e.Code)
			assert.Equal(t, "threads", e.Details["platform"])
		})
	}
}

func TestPostPageWithoutThePost(t *testing.T) {
	s, m := newSite(t)
	s.html("/@alice/post/NOPE", 200, "profile.html")
	_, err := scrape(m, post("alice", "NOPE"), 2, 0)
	assert.Equal(t, "upstream_error", mustErr(t, err).Code)
}

func TestNotFound(t *testing.T) {
	s, m := newSite(t)
	s.raw("/@nobody", 404, "")
	s.raw("/@alice/post/GONE", 404, "")
	_, err := scrape(m, profile("nobody"), 2, 0)
	assert.Equal(t, "account_not_found", mustErr(t, err).Code)
	_, err = scrape(m, post("alice", "GONE"), 2, 0)
	assert.Equal(t, "post_not_found", mustErr(t, err).Code)
}

func TestRateLimit(t *testing.T) {
	s, m := newSite(t)
	s.raw("/@alice", 429, "", "Retry-After", "120")
	_, err := scrape(m, profile("alice"), 2, 0)
	e := mustErr(t, err)
	assert.Equal(t, 429, e.Status)
	assert.Equal(t, "rate_limited", e.Code)
	assert.Equal(t, "120", e.Header.Get("Retry-After"))
	assert.Equal(t, 120, e.Details["retry_after_s"])

	s.raw("/@alice", 429, "")
	_, err = scrape(m, profile("alice"), 2, 0)
	assert.Empty(t, mustErr(t, err).Header.Get("Retry-After"))
}

func TestUpstreamFailures(t *testing.T) {
	s, m := newSite(t)
	s.raw("/@alice", 500, "")
	_, err := scrape(m, profile("alice"), 2, 0)
	assert.Equal(t, "upstream_error", mustErr(t, err).Code)

	s.raw("/@alice", 302, "", "Location", "https://evil.example/")
	_, err = scrape(m, profile("alice"), 2, 0)
	assert.Equal(t, "upstream_error", mustErr(t, err).Code, "redirects are not followed")

	router := scrapertest.New(t).Fail(host, errors.New("connection refused"))
	_, err = scrape(New(router.Client()), profile("alice"), 2, 0)
	assert.Equal(t, "upstream_error", mustErr(t, err).Code)
}

func TestTimeout(t *testing.T) {
	s, m := newSite(t)
	s.routes["/@alice"] = func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := m.Scrape(ctx, profile("alice"), ss.Request{MaxPosts: 2})
	e := mustErr(t, err)
	assert.Equal(t, 504, e.Status)
	assert.Equal(t, "upstream_timeout", e.Code)
}

func mustURL(t *testing.T, s string) *url.URL {
	u, err := url.Parse(s)
	require.NoError(t, err)
	return u
}
