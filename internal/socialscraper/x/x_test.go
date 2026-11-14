package x

import (
	"context"
	"encoding/json"
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
	authToken = "SENTINEL-auth-6f1c9d2b"
	ct0       = "SENTINEL-ct0-8a7e3b5d"
	postID    = "1790000000000000000"
)

var now = time.Unix(1_800_000_000, 0)

// site is x.com: handlers by GraphQL operation name, every request recorded with its decoded variables.
type site struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string]func(vars map[string]any) http.HandlerFunc
	reqs   []*http.Request
	vars   []map[string]any
}

func newSite(t *testing.T) (*site, *Module) {
	s := &site{t: t, routes: map[string]func(map[string]any) http.HandlerFunc{}}
	m := New(scrapertest.New(t).Handle("x.com", s).Client(), func(k string) string {
		return map[string]string{"TWITTER_AUTH_TOKEN": authToken, "TWITTER_CT0": ct0}[k]
	})
	m.Now = func() time.Time { return now }
	return s, m
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	var vars map[string]any
	_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &vars)
	s.mu.Lock()
	s.reqs, s.vars = append(s.reqs, r), append(s.vars, vars)
	route := s.routes[op]
	s.mu.Unlock()
	if route == nil {
		s.t.Errorf("unexpected operation %q", op)
		w.WriteHeader(500)
		return
	}
	route(vars)(w, r)
}

func (s *site) on(op string, f func(vars map[string]any) http.HandlerFunc) { s.routes[op] = f }

// serve answers op with fixed bodies; later calls without a cursor-specific body fall back to the first.
func (s *site) serve(op string, status int, body []byte, headers ...string) {
	s.on(op, func(map[string]any) http.HandlerFunc { return scrapertest.Body(status, body, headers...) })
}

// paged answers by the cursor variable ("" for the first page).
func (s *site) paged(op string, pages map[string][]byte) {
	s.on(op, func(vars map[string]any) http.HandlerFunc {
		c, _ := vars["cursor"].(string)
		b, ok := pages[c]
		if !ok {
			s.t.Errorf("%s: no page for cursor %q", op, c)
			return scrapertest.Body(500, nil)
		}
		return scrapertest.Body(200, b)
	})
}

func (s *site) count(op string) (n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.reqs {
		if strings.HasSuffix(r.URL.Path, "/"+op) {
			n++
		}
	}
	return n
}

func fx(t *testing.T, name string) []byte { return scrapertest.Fixture(t, name) }

func (s *site) profile(t *testing.T) {
	s.serve("UserByScreenName", 200, fx(t, "user_alice.json"))
	s.paged("UserTweets", map[string][]byte{"": fx(t, "usertweets_alice_1.json"), "TWEETS_P2": fx(t, "usertweets_alice_2.json")})
}

func (s *site) post(t *testing.T) {
	s.paged("TweetDetail", map[string][]byte{"": fx(t, "tweetdetail_1790.json"), "REPLIES_P2": fx(t, "tweetdetail_replies_2.json")})
	s.paged("UserTweets", map[string][]byte{"": fx(t, "usertweets_alice_1.json"), "TWEETS_P2": fx(t, "usertweets_alice_2.json")})
}

func profileT(u string) ss.Target {
	return ss.Target{Platform: "twitter", URLKind: "profile", IDs: map[string]string{"username": u}}
}

func postT() ss.Target {
	return ss.Target{Platform: "twitter", URLKind: "post", IDs: map[string]string{"username": "alice", "status_id": postID}}
}

func scrape(m *Module, t ss.Target, posts, comments int) (*ss.Result, error) {
	return m.Scrape(context.Background(), t, ss.Request{MaxPosts: posts, MaxComments: comments})
}

func typed(t *testing.T, err error) *ss.Error {
	t.Helper()
	var se *ss.Error
	require.ErrorAs(t, err, &se)
	return se
}

func codes(r *ss.Result) (c []string) {
	for _, w := range r.Warnings {
		c = append(c, w.Code)
	}
	return c
}

func ids(ps []ss.Post) (o []string) {
	for _, p := range ps {
		o = append(o, p.ID)
	}
	return o
}

func TestProfileSkipsRetweetsAndPinnedDuplicates(t *testing.T) {
	s, m := newSite(t)
	s.profile(t)
	r, err := scrape(m, profileT("alice"), 2, 5)
	require.NoError(t, err)
	assert.Equal(t, "twitter", r.Platform)
	assert.Equal(t, "profile", r.URLKind)
	assert.Equal(t, "alice", r.Username)
	assert.Equal(t, "Alice A.", *r.DisplayName)
	assert.Equal(t, "Birds & books.\nOpinions are mine.", *r.Bio)
	assert.Equal(t, []int{321, 12, 1500}, []int{*r.Followers, *r.Following, *r.PostCount})
	assert.True(t, *r.Verified)
	assert.Equal(t, "https://x.com/alice", *r.ProfileURL)
	assert.Equal(t, []string{postID, "1789000000000000000"}, ids(r.Posts)) // no retweet, newest first, capped
	assert.Equal(t, ss.Post{ID: postID, Text: "Hello & welcome <3", CreatedAt: ss.Ptr("2026-11-14T09:30:15Z"),
		URL: ss.Ptr("https://x.com/alice/status/" + postID), Likes: ss.Ptr(5), Replies: ss.Ptr(2), Reposts: ss.Ptr(1)}, r.Posts[0])
	assert.Nil(t, r.Post)
	assert.Empty(t, r.Comments)
	assert.Empty(t, r.Warnings)
	assert.Equal(t, 1, s.count("UserTweets"), "two own tweets were enough: no second page")
}

func TestProfileFollowsTheCursor(t *testing.T) {
	s, m := newSite(t)
	s.profile(t)
	r, err := scrape(m, profileT("alice"), 10, 5)
	require.NoError(t, err)
	assert.Equal(t, []string{postID, "1789000000000000000", "1788000000000000000"}, ids(r.Posts))
	assert.Equal(t, 2, s.count("UserTweets"))
	assert.Equal(t, "TWEETS_P2", s.vars[len(s.vars)-1]["cursor"])
	assert.Equal(t, "1001", s.vars[1]["userId"])
}

func TestRequestShape(t *testing.T) {
	s, m := newSite(t)
	s.profile(t)
	_, err := scrape(m, profileT("alice"), 10, 5)
	require.NoError(t, err)
	require.NotEmpty(t, s.reqs)
	for _, r := range s.reqs {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "x.com", r.URL.Host)
		assert.True(t, strings.HasPrefix(r.URL.Path, "/i/api/graphql/"), r.URL.Path)
		assert.Equal(t, "auth_token="+authToken+"; ct0="+ct0, r.Header.Get("Cookie"))
		assert.Equal(t, ct0, r.Header.Get("x-csrf-token"))
		assert.Equal(t, "Bearer "+bearer, r.Header.Get("Authorization"))
		assert.NotEmpty(t, r.URL.Query().Get("features"))
	}
	assert.Equal(t, "/i/api/graphql/"+opUserByScreenName.ID+"/UserByScreenName", s.reqs[0].URL.Path)
	assert.Equal(t, "alice", s.vars[0]["screen_name"])
}

func TestPostWithRepliesOldestFirst(t *testing.T) {
	s, m := newSite(t)
	s.post(t)
	r, err := scrape(m, postT(), 20, 50)
	require.NoError(t, err)
	assert.Equal(t, "post", r.URLKind)
	assert.Equal(t, "alice", r.Username)
	assert.Equal(t, postID, r.Post.ID)
	assert.Equal(t, "Hello & welcome <3", r.Post.Text)
	var texts []string
	for _, c := range r.Comments {
		texts = append(texts, c.Text)
		assert.Equal(t, postID, *c.InReplyToID)
	}
	// served 101, 100 (newest first) and 099 on the next page; the nested reply to 100 is not a direct reply
	assert.Equal(t, []string{"Oldest reply", "First reply", "Second reply"}, texts)
	assert.Equal(t, ss.Author{Username: "carol", DisplayName: ss.Ptr("Carol")}, r.Comments[1].Author)
	assert.Equal(t, 2, *r.Comments[1].Likes)
	assert.Len(t, r.Posts, 3)
	assert.Empty(t, r.Warnings)
	assert.Equal(t, 2, s.count("TweetDetail"))
	assert.Equal(t, postID, s.vars[0]["focalTweetId"])
}

func TestCommentsCappedOrSkipped(t *testing.T) {
	s, m := newSite(t)
	s.post(t)
	r, err := scrape(m, postT(), 20, 2)
	require.NoError(t, err)
	assert.Len(t, r.Comments, 2)
	assert.Equal(t, "First reply", r.Comments[0].Text) // the two served first, oldest first
	assert.Equal(t, 1, s.count("TweetDetail"), "two replies known: no further reply page")

	s2, m2 := newSite(t)
	s2.post(t)
	r, err = scrape(m2, postT(), 20, 0)
	require.NoError(t, err)
	assert.Empty(t, r.Comments)
	assert.Empty(t, r.Warnings)
	assert.Equal(t, 1, s2.count("TweetDetail"), "no reply request when max_comments is 0")
}

func TestWrappedLongFormTweet(t *testing.T) {
	s, m := newSite(t)
	s.serve("TweetDetail", 200, fx(t, "tweetdetail_wrapped.json"))
	s.serve("UserTweets", 200, fx(t, "usertweets_empty.json"))
	r, err := scrape(m, postT(), 5, 5)
	require.NoError(t, err)
	assert.Equal(t, "Long form text, uncut", r.Post.Text)
	assert.Empty(t, r.Posts)
}

func TestNotFoundAndProtected(t *testing.T) {
	tests := []struct {
		name, op, file, code string
		target               ss.Target
		status               int
	}{
		{"missing user", "UserByScreenName", "user_missing.json", "account_not_found", profileT("nobody"), 404},
		{"suspended user", "UserByScreenName", "user_suspended.json", "account_not_found", profileT("nobody"), 404},
		{"protected user", "UserByScreenName", "user_bob.json", "account_private", profileT("bob"), 403},
		{"missing tweet", "TweetDetail", "tweetdetail_missing.json", "post_not_found", postT(), 404},
		{"tombstone", "TweetDetail", "tweetdetail_tombstone.json", "post_not_found", postT(), 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, m := newSite(t)
			s.serve(tc.op, 200, fx(t, tc.file))
			_, err := scrape(m, tc.target, 5, 5)
			e := typed(t, err)
			assert.Equal(t, tc.code, e.Code)
			assert.Equal(t, tc.status, e.Status)
			assert.Equal(t, "twitter", e.Details["platform"])
			assert.Zero(t, s.count("UserTweets"))
		})
	}
}

func TestRateLimited(t *testing.T) {
	s, m := newSite(t)
	s.serve("UserByScreenName", 429, []byte(`{}`), "x-rate-limit-reset", "1800000090")
	_, err := scrape(m, profileT("alice"), 5, 5)
	e := typed(t, err)
	assert.Equal(t, "rate_limited", e.Code)
	assert.Equal(t, 429, e.Status)
	assert.Equal(t, 90, e.Details["retry_after_s"])
	assert.Equal(t, "90", e.Header.Get("Retry-After"))

	s, m = newSite(t)
	s.serve("UserByScreenName", 429, []byte(`{}`))
	_, err = scrape(m, profileT("alice"), 5, 5)
	e = typed(t, err)
	assert.Equal(t, "rate_limited", e.Code)
	assert.NotContains(t, e.Details, "retry_after_s")
	assert.Empty(t, e.Header.Get("Retry-After"))

	s, m = newSite(t) // a reset in the past is "retry now"
	s.serve("UserByScreenName", 429, []byte(`{}`), "x-rate-limit-reset", "1")
	_, err = scrape(m, profileT("alice"), 5, 5)
	assert.Equal(t, 0, typed(t, err).Details["retry_after_s"])
}

func TestRepliesFailureIsAWarning(t *testing.T) {
	s, m := newSite(t)
	s.on("TweetDetail", func(vars map[string]any) http.HandlerFunc {
		if vars["cursor"] == "REPLIES_P2" {
			return scrapertest.Body(500, []byte("boom "+authToken))
		}
		return scrapertest.Body(200, fx(t, "tweetdetail_focal_cursor.json"))
	})
	s.serve("UserTweets", 200, fx(t, "usertweets_empty.json"))
	r, err := scrape(m, postT(), 5, 5)
	require.NoError(t, err)
	assert.Equal(t, postID, r.Post.ID)
	assert.Empty(t, r.Comments)
	assert.Equal(t, []string{"comments_unavailable"}, codes(r))
}

func TestHistoryFailureIsAWarning(t *testing.T) {
	s, m := newSite(t)
	s.serve("UserByScreenName", 200, fx(t, "user_alice.json"))
	s.serve("UserTweets", 500, []byte("boom"))
	r, err := scrape(m, profileT("alice"), 5, 5)
	require.NoError(t, err)
	assert.Equal(t, "alice", r.Username)
	assert.Empty(t, r.Posts)
	assert.Equal(t, []string{"history_unavailable"}, codes(r))
}

func TestOtherFailuresAreUpstreamErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"cookies rejected 401", 401, `{"errors":[{"message":"` + ct0 + `"}]}`},
		{"cookies rejected 403", 403, `forbidden ` + authToken},
		{"server error", 500, `oops ` + authToken},
		{"not an object", 200, `[]`},
		{"not json", 200, `<html>` + ct0 + `</html>`},
		{"errors only", 200, `{"errors":[{"code":36,"message":"` + ct0 + `"}]}`},
		{"wrong shape", 200, `{"data":{"user":"` + authToken + `"}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, m := newSite(t)
			s.serve("UserByScreenName", tc.status, []byte(tc.body))
			_, err := scrape(m, profileT("alice"), 5, 5)
			e := typed(t, err)
			assert.Equal(t, "upstream_error", e.Code)
			assert.Equal(t, 502, e.Status)
			assert.NotContains(t, e.Error(), authToken)
			assert.NotContains(t, e.Error(), ct0)
		})
	}
}

func TestTransportFailureAndDeadline(t *testing.T) {
	router := scrapertest.New(t).Fail("x.com", &sentinelErr{})
	m := New(router.Client(), func(k string) string { return "v-" + k })
	_, err := scrape(m, profileT("alice"), 5, 5)
	e := typed(t, err)
	assert.Equal(t, "upstream_error", e.Code)
	assert.NotContains(t, e.Error(), "sentinel")

	s, m := newSite(t)
	s.on("UserByScreenName", func(map[string]any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = m.Scrape(ctx, profileT("alice"), ss.Request{MaxPosts: 5, MaxComments: 5})
	e = typed(t, err)
	assert.Equal(t, "upstream_timeout", e.Code)
	assert.Equal(t, 504, e.Status)
}

type sentinelErr struct{}

func (*sentinelErr) Error() string { return "dial: sentinel-upstream-text" }

func TestRedirectsAreNotFollowed(t *testing.T) {
	s, m := newSite(t)
	s.serve("UserByScreenName", 302, nil, "Location", "https://evil.example/")
	_, err := scrape(m, profileT("alice"), 5, 5) // an unrouted host would fail the test
	assert.Equal(t, "upstream_error", typed(t, err).Code)
}

func TestNotConfigured(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"both", nil, []string{"TWITTER_AUTH_TOKEN", "TWITTER_CT0"}},
		{"auth token", map[string]string{"TWITTER_CT0": ct0}, []string{"TWITTER_AUTH_TOKEN"}},
		{"ct0", map[string]string{"TWITTER_AUTH_TOKEN": authToken}, []string{"TWITTER_CT0"}},
		{"empty counts as unset", map[string]string{"TWITTER_AUTH_TOKEN": authToken, "TWITTER_CT0": " "}, []string{"TWITTER_CT0"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := scrapertest.New(t) // any request fails the test: nothing is routed
			m := New(router.Client(), func(k string) string { return tc.env[k] })
			_, err := scrape(m, profileT("alice"), 5, 5)
			e := typed(t, err)
			assert.Equal(t, "platform_not_configured", e.Code)
			assert.Equal(t, 503, e.Status)
			assert.Equal(t, "twitter", e.Details["platform"])
			assert.Equal(t, tc.want, e.Details["missing_settings"])
			assert.Empty(t, router.Requests())
			assert.NotContains(t, e.Message, authToken)
		})
	}
}
