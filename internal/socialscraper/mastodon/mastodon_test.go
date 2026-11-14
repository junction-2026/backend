package mastodon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/scrapertest"
)

const (
	host = "mastodon.example"
	sid  = "112233445566778899"
)

// instance is the Mastodon host: handlers by path, every request recorded.
type instance struct {
	t        *testing.T
	routes   map[string]http.HandlerFunc
	requests []string // "path?query"
}

func newInstance(t *testing.T) (*instance, *Module) {
	in := &instance{t: t, routes: map[string]http.HandlerFunc{}}
	return in, New(scrapertest.New(t).Handle(host, in).Client())
}

func (in *instance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	in.requests = append(in.requests, r.URL.RequestURI())
	h := in.routes[r.URL.Path]
	if h == nil {
		in.t.Errorf("unexpected request %s", r.URL)
		w.WriteHeader(500)
		return
	}
	h(w, r)
}

func (in *instance) on(path string, h http.HandlerFunc) { in.routes[path] = h }

func (in *instance) json(path string, status int, body []byte, headers ...string) {
	in.on(path, scrapertest.Body(status, body, headers...))
}

func (in *instance) count(path string) (n int) {
	for _, r := range in.requests {
		if strings.HasPrefix(r, path) {
			n++
		}
	}
	return n
}

func (in *instance) profile() {
	in.json("/api/v1/accounts/lookup", 200, scrapertest.Fixture(in.t, "account_alice.json"))
	in.json("/api/v1/accounts/1001/statuses", 200, scrapertest.Fixture(in.t, "statuses_page1.json"))
}

func profile() ss.Target {
	return ss.Target{Platform: "mastodon", URLKind: "profile", IDs: map[string]string{"host": host, "user": "alice"}}
}

func post() ss.Target {
	return ss.Target{Platform: "mastodon", URLKind: "post", IDs: map[string]string{"host": host, "user": "alice", "status_id": sid}}
}

func scrape(m *Module, t ss.Target, posts, comments int) (*ss.Result, error) {
	return m.Scrape(context.Background(), t, ss.Request{MaxPosts: posts, MaxComments: comments})
}

func mustErr(t *testing.T, err error) *ss.Error {
	t.Helper()
	var se *ss.Error
	require.True(t, errors.As(err, &se), "got %v", err)
	return se
}

func ids(posts []ss.Post) (out []string) {
	for _, p := range posts {
		out = append(out, p.ID)
	}
	return out
}

func fixtureMap(t *testing.T, name string) map[string]any {
	var m map[string]any
	require.NoError(t, json.Unmarshal(scrapertest.Fixture(t, name), &m))
	return m
}

func fixtureList(t *testing.T, name string) []map[string]any {
	var m []map[string]any
	require.NoError(t, json.Unmarshal(scrapertest.Fixture(t, name), &m))
	return m
}

func jsonOf(v any) []byte { b, _ := json.Marshal(v); return b }

func TestProfileScrape(t *testing.T) {
	in, m := newInstance(t)
	in.profile()
	res, err := scrape(m, profile(), 2, 50)
	require.NoError(t, err)

	assert.Equal(t, "mastodon", res.Platform)
	assert.Equal(t, "profile", res.URLKind)
	assert.Equal(t, "alice@mastodon.example", res.Username)
	assert.Equal(t, "Alice", *res.DisplayName)
	assert.Equal(t, "Birds & books.\nOpinions are mine.\nShe/her", *res.Bio)
	assert.Equal(t, 321, *res.Followers)
	assert.Equal(t, 12, *res.Following)
	assert.Equal(t, 1500, *res.PostCount)
	assert.Nil(t, res.Verified)
	assert.Equal(t, "https://mastodon.example/@alice", *res.ProfileURL)
	assert.Equal(t, []string{"5003", "5002"}, ids(res.Posts)) // newest first, capped
	assert.Equal(t, ss.Post{ID: "5003", Text: "Status 5003", CreatedAt: ss.Ptr("2026-11-14T09:30:15Z"),
		URL: ss.Ptr("https://mastodon.example/@alice/5003"), Likes: ss.Ptr(5), Replies: ss.Ptr(3), Reposts: ss.Ptr(2)}, res.Posts[0])
	assert.Nil(t, res.Post)
	assert.Empty(t, res.Comments)
	assert.Empty(t, res.Warnings)
	// the first page asks for only what is needed and no boosts
	last := in.requests[len(in.requests)-1]
	assert.Contains(t, last, "limit=2")
	assert.Contains(t, last, "exclude_reblogs=true")
	assert.Contains(t, in.requests[0], "acct=alice")
}

func TestLinkHeaderPagination(t *testing.T) {
	in, m := newInstance(t)
	in.json("/api/v1/accounts/lookup", 200, scrapertest.Fixture(t, "account_alice.json"))
	in.on("/api/v1/accounts/1001/statuses", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("max_id") == "5001" {
			scrapertest.Body(200, scrapertest.Fixture(t, "statuses_page2.json"))(w, r)
			return
		}
		scrapertest.Body(200, scrapertest.Fixture(t, "statuses_page1.json"), "Link",
			`<https://mastodon.example/api/v1/accounts/1001/statuses?max_id=5001&limit=40>; rel="next", `+
				`<https://mastodon.example/api/v1/accounts/1001/statuses?min_id=5003>; rel="prev"`)(w, r)
	})
	res, err := scrape(m, profile(), 4, 50)
	require.NoError(t, err)
	assert.Equal(t, []string{"5003", "5002", "5001", "5000"}, ids(res.Posts)) // stops at max_posts
	assert.Equal(t, 2, in.count("/api/v1/accounts/1001/statuses"))
}

func TestPaginationNeverLeavesTheInstance(t *testing.T) {
	for name, next := range map[string]string{
		"other host":   "https://evil.example/steal",
		"http":         "http://mastodon.example/api/v1/accounts/1001/statuses?max_id=5001",
		"other port":   "https://mastodon.example:8443/api/v1/accounts/1001/statuses?max_id=5001",
		"relative":     "/api/v1/accounts/1001/statuses?max_id=5001",
		"userinfo-ish": "https://mastodon.example.evil.example/x",
	} {
		t.Run(name, func(t *testing.T) {
			in, m := newInstance(t) // an unrouted host or a second statuses request would fail the test
			in.json("/api/v1/accounts/lookup", 200, scrapertest.Fixture(t, "account_alice.json"))
			in.json("/api/v1/accounts/1001/statuses", 200, scrapertest.Fixture(t, "statuses_page1.json"), "Link", `<`+next+`>; rel="next"`)
			res, err := scrape(m, profile(), 20, 50)
			require.NoError(t, err)
			assert.Len(t, res.Posts, 3)
			assert.Equal(t, 1, in.count("/api/v1/accounts/1001/statuses"))
		})
	}
}

func TestHistoryWarnings(t *testing.T) {
	in, m := newInstance(t)
	in.json("/api/v1/accounts/lookup", 200, scrapertest.Fixture(t, "account_alice.json"))
	in.on("/api/v1/accounts/1001/statuses", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("max_id") == "5001" {
			scrapertest.Body(503, nil)(w, r)
			return
		}
		scrapertest.Body(200, scrapertest.Fixture(t, "statuses_page1.json"), "Link",
			`<https://mastodon.example/api/v1/accounts/1001/statuses?max_id=5001>; rel="next"`)(w, r)
	})
	res, err := scrape(m, profile(), 20, 50)
	require.NoError(t, err)
	assert.Len(t, res.Posts, 3)
	require.Len(t, res.Warnings, 1)
	assert.Equal(t, "history_truncated", res.Warnings[0].Code)

	in.json("/api/v1/accounts/1001/statuses", 403, []byte(`{"error":"nope"}`))
	res, err = scrape(m, profile(), 20, 50)
	require.NoError(t, err)
	assert.Empty(t, res.Posts)
	require.Len(t, res.Warnings, 1)
	assert.Equal(t, "history_unavailable", res.Warnings[0].Code)
}

func TestPostScrapeAndHTMLStripping(t *testing.T) {
	in, m := newInstance(t)
	in.json("/api/v1/statuses/"+sid, 200, scrapertest.Fixture(t, "status_"+sid+".json"))
	in.json("/api/v1/statuses/"+sid+"/context", 200, scrapertest.Fixture(t, "context.json"))
	in.json("/api/v1/accounts/1001/statuses", 200, scrapertest.Fixture(t, "statuses_page1.json"))
	res, err := scrape(m, post(), 20, 50)
	require.NoError(t, err)

	assert.Equal(t, "post", res.URLKind)
	assert.Equal(t, "alice@mastodon.example", res.Username)
	assert.Equal(t, sid, res.Post.ID)
	assert.Equal(t, "Hello & welcome\nBye", res.Post.Text)
	var got []string
	for _, c := range res.Comments {
		got = append(got, c.ID)
	}
	assert.Equal(t, []string{"6001", "6002", "6003"}, got) // oldest first
	assert.Equal(t, ss.Author{Username: "bob@mastodon.example", DisplayName: ss.Ptr("Bob")}, res.Comments[0].Author)
	assert.Equal(t, ss.Author{Username: "carol@other.example"}, res.Comments[1].Author)
	assert.Equal(t, "6001", *res.Comments[1].InReplyToID)
	assert.Equal(t, sid, *res.Comments[0].InReplyToID)
	assert.Len(t, res.Posts, 3)
}

func TestMaxCommentsCapsAndZeroSkipsContext(t *testing.T) {
	in, m := newInstance(t)
	in.json("/api/v1/statuses/"+sid, 200, scrapertest.Fixture(t, "status_"+sid+".json"))
	in.json("/api/v1/statuses/"+sid+"/context", 200, scrapertest.Fixture(t, "context.json"))
	in.json("/api/v1/accounts/1001/statuses", 200, []byte(`[]`))
	res, err := scrape(m, post(), 20, 2)
	require.NoError(t, err)
	assert.Len(t, res.Comments, 2)
	assert.Equal(t, "6001", res.Comments[0].ID)

	in.requests = nil
	res, err = scrape(m, post(), 20, 0)
	require.NoError(t, err)
	assert.Empty(t, res.Comments)
	assert.Empty(t, res.Warnings)
	assert.Zero(t, in.count("/api/v1/statuses/"+sid+"/context"))
}

func TestContextFailureIsCommentsUnavailable(t *testing.T) {
	in, m := newInstance(t)
	in.json("/api/v1/statuses/"+sid, 200, scrapertest.Fixture(t, "status_"+sid+".json"))
	in.json("/api/v1/statuses/"+sid+"/context", 502, nil)
	in.json("/api/v1/accounts/1001/statuses", 200, []byte(`[]`))
	res, err := scrape(m, post(), 20, 50)
	require.NoError(t, err)
	assert.Empty(t, res.Comments)
	assert.Equal(t, sid, res.Post.ID)
	require.Len(t, res.Warnings, 1)
	assert.Equal(t, "comments_unavailable", res.Warnings[0].Code)
}

func TestMissingCountersAreNull(t *testing.T) {
	in, m := newInstance(t)
	acct := fixtureMap(t, "account_alice.json")
	delete(acct, "followers_count")
	in.json("/api/v1/accounts/lookup", 200, jsonOf(acct))
	in.json("/api/v1/accounts/1001/statuses", 200, []byte(`[{"id": "1", "content": ""}]`))
	res, err := scrape(m, profile(), 20, 50)
	require.NoError(t, err)
	assert.Nil(t, res.Followers)
	assert.Equal(t, ss.Post{ID: "1"}, res.Posts[0]) // text "" , counters and times null
}

func TestProfileErrorMapping(t *testing.T) {
	for name, c := range map[string]struct {
		h      http.HandlerFunc
		status int
		code   string
	}{
		"404 json":      {scrapertest.Body(404, []byte(`{"error":"Record not found"}`)), 404, "account_not_found"},
		"410":           {scrapertest.Body(410, []byte(`{"error":"gone"}`)), 404, "account_not_found"},
		"401":           {scrapertest.Body(401, []byte(`{"error":"login required"}`)), 403, "account_private"},
		"403":           {scrapertest.Body(403, []byte(`{"error":"private"}`)), 403, "account_private"},
		"429":           {scrapertest.Body(429, nil, "Retry-After", "42"), 429, "rate_limited"},
		"500":           {scrapertest.Body(500, []byte("boom"), "Content-Type", "text/plain"), 502, "upstream_error"},
		"array body":    {scrapertest.Body(200, []byte(`[1, 2]`)), 502, "upstream_error"},
		"404 html":      {scrapertest.Body(404, []byte(`<html>Not Found</html>`), "Content-Type", "text/html"), 422, "unsupported_url"},
		"200 html":      {scrapertest.Body(200, []byte(`<html>hi</html>`), "Content-Type", "text/html"), 422, "unsupported_url"},
		"200 scalar":    {scrapertest.Body(200, []byte(`"hi"`)), 422, "unsupported_url"},
		"redirect":      {scrapertest.Body(301, nil, "Location", "https://x.example/"), 422, "unsupported_url"},
		"redirect self": {scrapertest.Body(302, nil, "Location", "https://mastodon.example/elsewhere"), 422, "unsupported_url"},
	} {
		t.Run(name, func(t *testing.T) {
			in, m := newInstance(t)
			in.on("/api/v1/accounts/lookup", c.h)
			_, err := scrape(m, profile(), 20, 50)
			se := mustErr(t, err)
			assert.Equal(t, c.status, se.Status)
			assert.Equal(t, c.code, se.Code)
			assert.Equal(t, "mastodon", se.Details["platform"])
			if c.code == "rate_limited" {
				assert.Equal(t, "42", se.Header.Get("Retry-After"))
				assert.Equal(t, 42, se.Details["retry_after_s"])
			}
			assert.Equal(t, 1, len(in.requests), "a redirect must not be followed")
		})
	}
}

func TestPostNotFound(t *testing.T) {
	in, m := newInstance(t)
	in.json("/api/v1/statuses/"+sid, 404, []byte(`{"error":"Record not found"}`))
	_, err := scrape(m, post(), 20, 50)
	assert.Equal(t, "post_not_found", mustErr(t, err).Code)
}

func TestTimeoutAndConnectError(t *testing.T) {
	slow := scrapertest.New(t).Handle(host, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := New(slow.Client()).Scrape(ctx, profile(), ss.Request{MaxPosts: 20})
	assert.Equal(t, "upstream_timeout", mustErr(t, err).Code)

	down := scrapertest.New(t).Fail(host, errors.New("connection refused"))
	_, err = New(down.Client()).Scrape(context.Background(), profile(), ss.Request{MaxPosts: 20})
	assert.Equal(t, "upstream_error", mustErr(t, err).Code)
	assert.Equal(t, 502, mustErr(t, err).Status)
}

func TestMalformedRequestedObjectIsUpstreamError(t *testing.T) {
	for name, c := range map[string]struct {
		target ss.Target
		path   string
		body   func(t *testing.T) []byte
	}{
		"account without id": {profile(), "/api/v1/accounts/lookup", func(*testing.T) []byte { return []byte(`{"unexpected": true}`) }},
		"wrong-typed counter": {profile(), "/api/v1/accounts/lookup", func(t *testing.T) []byte {
			a := fixtureMap(t, "account_alice.json")
			a["followers_count"] = "abc"
			return jsonOf(a)
		}},
		"status account without id": {post(), "/api/v1/statuses/" + sid, func(t *testing.T) []byte {
			s := fixtureMap(t, "status_"+sid+".json")
			delete(s["account"].(map[string]any), "id")
			return jsonOf(s)
		}},
		"status without id": {post(), "/api/v1/statuses/" + sid, func(t *testing.T) []byte {
			s := fixtureMap(t, "status_"+sid+".json")
			delete(s, "id")
			return jsonOf(s)
		}},
		"status without account": {post(), "/api/v1/statuses/" + sid, func(t *testing.T) []byte { return []byte(`{"id":"1"}`) }},
	} {
		t.Run(name, func(t *testing.T) {
			in, m := newInstance(t)
			in.json(c.path, 200, c.body(t))
			_, err := scrape(m, c.target, 20, 50)
			se := mustErr(t, err)
			assert.Equal(t, 502, se.Status)
			assert.Equal(t, "upstream_error", se.Code)
			assert.Equal(t, "mastodon", se.Details["platform"])
		})
	}
}

func TestBadHistoryItemIsSkippedWithPartialParse(t *testing.T) {
	in, m := newInstance(t)
	page := fixtureList(t, "statuses_page1.json")
	page[1]["favourites_count"] = "many"
	page = append(page, map[string]any{"content": "no id"})
	in.json("/api/v1/accounts/lookup", 200, scrapertest.Fixture(t, "account_alice.json"))
	in.json("/api/v1/accounts/1001/statuses", 200, jsonOf(page))
	res, err := scrape(m, profile(), 20, 50)
	require.NoError(t, err)
	assert.Equal(t, []string{"5003", "5001"}, ids(res.Posts))
	require.Len(t, res.Warnings, 1)
	assert.Equal(t, "partial_parse", res.Warnings[0].Code)
}

func TestBadContextItemIsSkippedWithPartialParse(t *testing.T) {
	in, m := newInstance(t)
	ctxBody := fixtureMap(t, "context.json")
	ctxBody["descendants"].([]any)[0].(map[string]any)["content"] = 42
	in.json("/api/v1/statuses/"+sid, 200, scrapertest.Fixture(t, "status_"+sid+".json"))
	in.json("/api/v1/statuses/"+sid+"/context", 200, jsonOf(ctxBody))
	in.json("/api/v1/accounts/1001/statuses", 200, []byte(`[]`))
	res, err := scrape(m, post(), 20, 50)
	require.NoError(t, err)
	var got []string
	for _, c := range res.Comments {
		got = append(got, c.ID)
	}
	assert.Equal(t, []string{"6001", "6003"}, got)
	require.Len(t, res.Warnings, 1)
	assert.Equal(t, "partial_parse", res.Warnings[0].Code)
}

func TestRedirectsAreNeverFollowed(t *testing.T) {
	// Even a client that follows redirects is replaced: New forces ErrUseLastResponse.
	in, m := newInstance(t)
	in.on("/api/v1/accounts/lookup", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/api/v1/accounts/lookup", http.StatusFound)
	})
	_, err := scrape(m, profile(), 20, 50)
	assert.Equal(t, "unsupported_url", mustErr(t, err).Code)
	assert.Len(t, in.requests, 1)
}

func TestIPLiteralAndDotlessHostsAreNotRecognized(t *testing.T) {
	for _, u := range []string{"https://127.0.0.1/@alice", "https://localhost/@alice", "https://[::1]/@alice", "https://2130706433/@alice"} {
		_, err := ss.Recognize(u)
		require.NotNil(t, err, u)
		assert.Equal(t, "unsupported_url", err.Code, u)
	}
}

// threads.net has no special case: a Mastodon-shaped path is probed like any host, and a non-Mastodon
// answer (HTML 404 or a redirect) is unsupported_url.
func TestFormerlyListedHostIsProbedLikeAnyHost(t *testing.T) {
	target, rerr := ss.Recognize("https://www.threads.net/@zuck")
	require.Nil(t, rerr)
	assert.Equal(t, map[string]string{"host": "threads.net", "user": "zuck"}, target.IDs)
	for name, h := range map[string]http.HandlerFunc{
		"404 html": scrapertest.Body(404, []byte(`<html>Not Found</html>`), "Content-Type", "text/html"),
		"redirect": scrapertest.Body(301, nil, "Location", "https://www.threads.net/@zuck"),
	} {
		t.Run(name, func(t *testing.T) {
			var paths []string
			router := scrapertest.New(t).Handle("threads.net", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				h(w, r)
			}))
			_, err := New(router.Client()).Scrape(context.Background(), target, ss.Request{MaxPosts: 20})
			assert.Equal(t, "unsupported_url", mustErr(t, err).Code)
			assert.Equal(t, []string{"/api/v1/accounts/lookup"}, paths)
		})
	}
}
