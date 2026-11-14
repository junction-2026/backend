package instagram

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/scrapertest"
)

const sentinel = "SENTINEL-sessionid-4f9a"

// site is Instagram: handlers by "host/path", every request recorded.
type site struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	seen   []*http.Request
}

func newSite(t *testing.T) *site {
	s := &site{t: t, routes: map[string]http.HandlerFunc{}}
	s.json(webHost+"/api/v1/users/web_profile_info/", 200, "web_profile_info_alice.json")
	s.on(mobileHost+"/api/v1/feed/user/1001/", func(w http.ResponseWriter, r *http.Request) {
		name := "feed_alice_page1.json"
		if r.URL.Query().Get("max_id") == "cursor-2" {
			name = "feed_alice_page2.json"
		}
		scrapertest.Body(200, scrapertest.Fixture(t, name))(w, r)
	})
	for _, c := range []string{"CpA111", "CpB222", "CpC333"} {
		id, ok := mediaID(c)
		require.True(t, ok)
		s.json(mobileHost+"/api/v1/media/"+id+"/info/", 200, "media_"+c+".json")
	}
	return s
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.seen = append(s.seen, r)
	h := s.routes[r.URL.Host+r.URL.Path]
	s.mu.Unlock()
	if h == nil {
		s.t.Errorf("unexpected request %s", r.URL)
		w.WriteHeader(500)
		return
	}
	h(w, r)
}

func (s *site) on(key string, h http.HandlerFunc) { s.routes[key] = h }

func (s *site) json(key string, status int, fixture string, headers ...string) {
	s.on(key, scrapertest.Body(status, scrapertest.Fixture(s.t, fixture), headers...))
}

func (s *site) raw(key string, status int, body string, headers ...string) {
	s.on(key, scrapertest.Body(status, []byte(body), headers...))
}

func (s *site) count(prefix string) (n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.seen {
		if strings.HasPrefix(r.URL.Host+r.URL.Path, prefix) {
			n++
		}
	}
	return n
}

func (s *site) module(session string, log *bytes.Buffer) *Module {
	router := scrapertest.New(s.t).Handle(webHost, s).Handle(mobileHost, s)
	var l = discard
	if log != nil {
		l = ss.NewLogger(log, ss.NewRedactor(func(string) string { return "" }, nil))
	}
	return New(router.Client(), session, l)
}

var discard = ss.NewLogger(&bytes.Buffer{}, ss.NewRedactor(func(string) string { return "" }, nil))

func writeSession(t *testing.T, content string) string {
	p := filepath.Join(t.TempDir(), "session.json")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func goodSession(t *testing.T) string {
	return writeSession(t, `{"sessionid":"`+sentinel+`","csrftoken":"csrf-tok","ds_user_id":"77"}`)
}

func profile() ss.Target {
	return ss.Target{Platform: "instagram", URLKind: "profile", IDs: map[string]string{"username": "alice"}}
}

func post(code string) ss.Target {
	return ss.Target{Platform: "instagram", URLKind: "post", IDs: map[string]string{"shortcode": code}}
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

func TestPublicProfile(t *testing.T) {
	s := newSite(t)
	res, err := scrape(s.module("", nil), profile(), 2, 5)
	require.NoError(t, err)
	assert.Equal(t, "instagram", res.Platform)
	assert.Equal(t, "profile", res.URLKind)
	assert.Equal(t, "alice", res.Username)
	assert.Equal(t, "Alice Example", *res.DisplayName)
	assert.Equal(t, "Birds & books.\nOpinions are mine.", *res.Bio)
	assert.Equal(t, "https://www.instagram.com/alice/", *res.ProfileURL)
	assert.Equal(t, 4321, *res.Followers)
	assert.Equal(t, 87, *res.Following)
	assert.Equal(t, 3, *res.PostCount)
	assert.True(t, *res.Verified)
	require.Len(t, res.Posts, 2)
	assert.Equal(t, "CpC333", res.Posts[0].ID)
	assert.Equal(t, "CpB222", res.Posts[1].ID)
	assert.Equal(t, ss.Post{ID: "CpC333", Text: "Third post", CreatedAt: ss.Ptr("2025-11-14T08:50:15Z"),
		URL: ss.Ptr("https://www.instagram.com/p/CpC333/"), Likes: ss.Ptr(30), Replies: ss.Ptr(2)}, res.Posts[0])
	assert.Nil(t, res.Post)
	assert.Empty(t, res.Comments)
	assert.Empty(t, res.Warnings)
	assert.Equal(t, 1, s.count(mobileHost+"/api/v1/feed/"), "enough posts after page one: no further page")
	assert.Zero(t, s.count(mobileHost+"/api/v1/media/"))
}

func TestOnlyInstagramHostsAndWebHeaders(t *testing.T) {
	s := newSite(t)
	_, err := scrape(s.module("", nil), post("CpB222"), 3, 5)
	require.NoError(t, err)
	require.NotEmpty(t, s.seen)
	for _, r := range s.seen {
		assert.Contains(t, []string{webHost, mobileHost}, r.URL.Host)
		assert.Equal(t, webAppID, r.Header.Get("X-IG-App-ID"))
		assert.Empty(t, r.Header.Get("Cookie"), "anonymous requests carry no cookie")
	}
}

func TestHistoryPagingHiddenLikesAndMissingCounts(t *testing.T) {
	s := newSite(t)
	res, err := scrape(s.module("", nil), profile(), 3, 0)
	require.NoError(t, err)
	require.Len(t, res.Posts, 3)
	assert.Equal(t, 2, s.count(mobileHost+"/api/v1/feed/"))
	last := res.Posts[2]
	assert.Equal(t, "CpA111", last.ID)
	assert.Equal(t, "", last.Text, "no caption: empty text")
	assert.Nil(t, last.Likes, "hidden likes are null, not 0")
	assert.Nil(t, last.Replies, "no comment_count: null, not 0")
	assert.Nil(t, last.Reposts)
	assert.Equal(t, 20, *res.Posts[1].Likes)
	assert.Empty(t, res.Warnings, "an account with fewer posts than asked for is not truncated")
}

func TestMissingCountersAreNullWithPartialParse(t *testing.T) {
	s := newSite(t)
	s.json(webHost+"/api/v1/users/web_profile_info/", 200, "web_profile_info_no_counters.json")
	res, err := scrape(s.module("", nil), profile(), 1, 0)
	require.NoError(t, err)
	assert.Nil(t, res.Followers)
	assert.Nil(t, res.Following)
	assert.Nil(t, res.PostCount)
	assert.Nil(t, res.Verified)
	assert.Equal(t, "Birds & books.\nOpinions are mine.", *res.Bio)
	require.Equal(t, []string{"partial_parse"}, codes(res.Warnings))
	assert.Contains(t, res.Warnings[0].Message, "followers, following, post_count")
}

func TestPostAnonymousHasNoComments(t *testing.T) {
	s := newSite(t)
	res, err := scrape(s.module("", nil), post("CpB222"), 3, 5)
	require.NoError(t, err)
	assert.Equal(t, "post", res.URLKind)
	assert.Equal(t, "alice", res.Username)
	assert.Equal(t, "CpB222", res.Post.ID)
	assert.Equal(t, 1, *res.Post.Replies)
	assert.Empty(t, res.Comments)
	assert.Equal(t, []string{"comments_unavailable"}, codes(res.Warnings))
	assert.Len(t, res.Posts, 3)
	assert.Equal(t, 4321, *res.Followers)
	assert.Zero(t, s.count(mobileHost+"/api/v1/media/"+mustID(t, "CpB222")+"/comments"), "no comments request without a session")
}

func mustID(t *testing.T, code string) string {
	id, ok := mediaID(code)
	require.True(t, ok)
	return id
}

func commentRoutes(t *testing.T, s *site) {
	id := mustID(t, "CpB222")
	s.json(mobileHost+"/api/v1/media/"+id+"/comments/", 200, "comments_CpB222.json")
	s.json(mobileHost+"/api/v1/media/"+id+"/comments/9002/child_comments/", 200, "child_comments_9002.json")
}

func TestCommentsWithASession(t *testing.T) {
	s := newSite(t)
	commentRoutes(t, s)
	m := s.module(goodSession(t), nil)

	res, err := scrape(m, post("CpB222"), 1, 2)
	require.NoError(t, err)
	require.Len(t, res.Comments, 2)
	assert.Equal(t, "9001", res.Comments[0].ID)
	assert.Equal(t, "first parent", res.Comments[0].Text)
	assert.Equal(t, "erin", res.Comments[0].Author.Username)
	assert.Equal(t, 5, *res.Comments[0].Likes)
	assert.Equal(t, "2025-11-13T09:10:00Z", *res.Comments[0].CreatedAt)
	assert.Nil(t, res.Comments[0].InReplyToID)
	assert.Equal(t, "9002", res.Comments[1].ID)
	assert.Equal(t, "Carol C", *res.Comments[1].Author.DisplayName)
	assert.Empty(t, res.Warnings)

	res, err = scrape(m, post("CpB222"), 1, 10)
	require.NoError(t, err)
	var got []string
	for _, c := range res.Comments {
		reply := "-"
		if c.InReplyToID != nil {
			reply = *c.InReplyToID
		}
		got = append(got, c.ID+"<"+reply)
	}
	assert.Equal(t, []string{"9001<-", "9002<-", "9003<9002", "9004<9002"}, got, "oldest first, replies name their parent")
}

func TestSessionCookiesAreSentToInstagramOnly(t *testing.T) {
	s := newSite(t)
	commentRoutes(t, s)
	_, err := scrape(s.module(goodSession(t), nil), post("CpB222"), 1, 2)
	require.NoError(t, err)
	for _, r := range s.seen {
		assert.Equal(t, "sessionid="+sentinel+"; csrftoken=csrf-tok; ds_user_id=77", r.Header.Get("Cookie"))
		assert.Equal(t, "csrf-tok", r.Header.Get("X-CSRFToken"))
	}
}

func TestBrokenSessionFileScrapesAnonymously(t *testing.T) {
	for name, file := range map[string]func() string{
		"missing":      func() string { return filepath.Join(t.TempDir(), "nope.json") },
		"not json":     func() string { return writeSession(t, "not a session") },
		"no sessionid": func() string { return writeSession(t, `{"csrftoken":"x"}`) },
		"bad cookie":   func() string { return writeSession(t, `{"sessionid":"a;b"}`) },
		"directory":    func() string { return t.TempDir() },
	} {
		t.Run(name, func(t *testing.T) {
			s := newSite(t)
			path := file()
			var log bytes.Buffer
			res, err := scrape(s.module(path, &log), post("CpB222"), 1, 5)
			require.NoError(t, err)
			assert.Equal(t, []string{"comments_unavailable"}, codes(res.Warnings))
			for _, r := range s.seen {
				assert.Empty(t, r.Header.Get("Cookie"))
			}
			assert.Equal(t, 1, strings.Count(log.String(), "\n"), "one warning line: %s", log.String())
			assert.Contains(t, log.String(), "scraping anonymously")
			assert.NotContains(t, log.String(), path)
		})
	}
	t.Run("unset", func(t *testing.T) {
		var log bytes.Buffer
		newSite(t).module("", &log)
		assert.Empty(t, log.String())
	})
}

func TestSessionAndPathNeverLeak(t *testing.T) {
	s := newSite(t)
	commentRoutes(t, s)
	s.raw(mobileHost+"/api/v1/media/"+mustID(t, "CpB222")+"/comments/9002/child_comments/", 500, "boom "+sentinel)
	path := goodSession(t)
	var log bytes.Buffer
	res, err := scrape(s.module(path, &log), post("CpB222"), 1, 10)
	require.NoError(t, err)
	b, _ := json.Marshal(res)
	assert.NotContains(t, string(b), sentinel)
	assert.NotContains(t, string(b), path)
	assert.NotContains(t, log.String(), sentinel)
	assert.NotContains(t, log.String(), path)
	assert.Equal(t, []string{"comments_unavailable"}, codes(res.Warnings))

	s.raw(webHost+"/api/v1/users/web_profile_info/", 500, sentinel)
	_, err = scrape(s.module(path, &log), profile(), 1, 1)
	assert.NotContains(t, mustErr(t, err).Message, sentinel)
}

func TestCommentsCutShortKeepWhatWasRead(t *testing.T) {
	s := newSite(t)
	id := mustID(t, "CpB222")
	page := 0
	s.on(mobileHost+"/api/v1/media/"+id+"/comments/", func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			scrapertest.Body(200, []byte(`{"comments":[{"pk":"1","text":"a","created_at":100,"user":{"username":"u"}}],"has_more_comments":true,"next_min_id":"c2"}`))(w, r)
			return
		}
		assert.Equal(t, "c2", r.URL.Query().Get("min_id"))
		scrapertest.Body(500, []byte(`{}`))(w, r)
	})
	res, err := scrape(s.module(goodSession(t), nil), post("CpB222"), 1, 10)
	require.NoError(t, err)
	require.Len(t, res.Comments, 1)
	assert.Equal(t, "1", res.Comments[0].ID)
	assert.Equal(t, []string{"comments_unavailable"}, codes(res.Warnings))
	assert.Equal(t, "CpB222", res.Post.ID)
}

func TestCommentsRefusedKeepThePost(t *testing.T) {
	s := newSite(t)
	s.raw(mobileHost+"/api/v1/media/"+mustID(t, "CpB222")+"/comments/", 403, `{"message":"login_required"}`)
	res, err := scrape(s.module(goodSession(t), nil), post("CpB222"), 1, 10)
	require.NoError(t, err)
	assert.Empty(t, res.Comments)
	assert.Equal(t, []string{"comments_unavailable"}, codes(res.Warnings))
}

func TestPrivateAccount(t *testing.T) {
	s := newSite(t)
	s.json(webHost+"/api/v1/users/web_profile_info/", 200, "web_profile_info_private.json")
	_, err := scrape(s.module("", nil), profile(), 2, 0)
	e := mustErr(t, err)
	assert.Equal(t, 403, e.Status)
	assert.Equal(t, "account_private", e.Code)
	assert.Zero(t, s.count(mobileHost))
}

func TestThrottledAndLoginWall(t *testing.T) {
	cases := map[string]struct {
		status  int
		body    string
		headers []string
		retry   string
	}{
		"429":               {429, `{"message":"Please wait a few minutes","status":"fail"}`, nil, ""},
		"429 with wait":     {429, `{}`, []string{"Retry-After", "90"}, "90"},
		"redirect to login": {302, ``, []string{"Location", "https://www.instagram.com/accounts/login/?next=/alice/"}, ""},
		"login body 400":    {400, `{"message":"login_required","require_login":true,"status":"fail"}`, nil, ""},
		"401":               {401, `{}`, nil, ""},
		"login page 200":    {200, `<html><a href="/accounts/login/">Log in</a></html>`, nil, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newSite(t)
			s.raw(webHost+"/api/v1/users/web_profile_info/", c.status, c.body, c.headers...)
			_, err := scrape(s.module("", nil), profile(), 2, 0)
			e := mustErr(t, err)
			assert.Equal(t, 429, e.Status)
			assert.Equal(t, "rate_limited", e.Code)
			assert.Equal(t, c.retry, e.Header.Get("Retry-After"))
		})
	}
}

func TestNotFound(t *testing.T) {
	s := newSite(t)
	s.raw(webHost+"/api/v1/users/web_profile_info/", 404, `{"message":"Not found"}`)
	_, err := scrape(s.module("", nil), profile(), 2, 0)
	assert.Equal(t, "account_not_found", mustErr(t, err).Code)

	s.json(webHost+"/api/v1/users/web_profile_info/", 200, "web_profile_info_missing.json")
	_, err = scrape(s.module("", nil), profile(), 2, 0)
	assert.Equal(t, 404, mustErr(t, err).Status)
	assert.Equal(t, "account_not_found", mustErr(t, err).Code)

	id := mustID(t, "CpZ999")
	s.json(mobileHost+"/api/v1/media/"+id+"/info/", 200, "media_empty.json")
	_, err = scrape(s.module("", nil), post("CpZ999"), 2, 0)
	assert.Equal(t, 404, mustErr(t, err).Status)
	assert.Equal(t, "post_not_found", mustErr(t, err).Code)

	s.raw(mobileHost+"/api/v1/media/"+id+"/info/", 404, `{}`)
	_, err = scrape(s.module("", nil), post("CpZ999"), 2, 0)
	assert.Equal(t, "post_not_found", mustErr(t, err).Code)

	_, err = scrape(s.module("", nil), post("not*a*code"), 2, 0)
	assert.Equal(t, "post_not_found", mustErr(t, err).Code)
}

func TestTimeout(t *testing.T) {
	s := newSite(t)
	s.on(webHost+"/api/v1/users/web_profile_info/", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.module("", nil).Scrape(ctx, profile(), ss.Request{MaxPosts: 2})
	e := mustErr(t, err)
	assert.Equal(t, 504, e.Status)
	assert.Equal(t, "upstream_timeout", e.Code)
}

func TestOtherFailuresAreUpstreamErrors(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
	}{
		"500":              {500, `<html>oops</html>`},
		"not json":         {200, `nope`},
		"html":             {200, `<html>maintenance</html>`},
		"403 without wall": {403, `{"message":"blocked"}`},
		"redirect":         {301, ``},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSite(t)
			s.raw(webHost+"/api/v1/users/web_profile_info/", c.status, c.body, "Location", "https://evil.example/")
			_, err := scrape(s.module("", nil), profile(), 2, 0)
			e := mustErr(t, err)
			assert.Equal(t, 502, e.Status)
			assert.Equal(t, "upstream_error", e.Code)
		})
	}
}

func TestHistoryFailureKeepsThePartialResult(t *testing.T) {
	s := newSite(t)
	s.on(mobileHost+"/api/v1/feed/user/1001/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("max_id") == "cursor-2" {
			scrapertest.Body(429, []byte(`{}`))(w, r)
			return
		}
		scrapertest.Body(200, scrapertest.Fixture(t, "feed_alice_page1.json"))(w, r)
	})
	res, err := scrape(s.module("", nil), profile(), 3, 0)
	require.NoError(t, err)
	require.Len(t, res.Posts, 2)
	assert.Equal(t, []string{"history_truncated"}, codes(res.Warnings))

	s.raw(mobileHost+"/api/v1/feed/user/1001/", 500, `{}`)
	res, err = scrape(s.module("", nil), profile(), 3, 0)
	require.NoError(t, err)
	assert.Empty(t, res.Posts)
	assert.Equal(t, "alice", res.Username)
	assert.Equal(t, []string{"history_unavailable"}, codes(res.Warnings))
}

func TestHistoryWithoutNextCursorIsTruncated(t *testing.T) {
	s := newSite(t)
	s.raw(mobileHost+"/api/v1/feed/user/1001/", 200, `{"items":[{"code":"X1","taken_at":5,"user":{}},{"taken_at":6}],"more_available":true}`)
	res, err := scrape(s.module("", nil), profile(), 5, 0)
	require.NoError(t, err)
	require.Len(t, res.Posts, 1)
	assert.Equal(t, []string{"history_truncated", "partial_parse"}, codes(res.Warnings))
}

func TestMediaID(t *testing.T) {
	for in, want := range map[string]string{"A": "0", "B": "1", "BA": "64", "-": "62", "_": "63", "Bg": "96"} {
		got, ok := mediaID(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "a!", "____________", "___________"} {
		_, ok := mediaID(bad)
		assert.False(t, ok, bad)
	}
	long, ok := mediaID("BAAAAAAAAAAXYZ") // only the first 11 characters count
	assert.True(t, ok)
	short, _ := mediaID("BAAAAAAAAAA")
	assert.Equal(t, short, long)
}

func TestUserTextNamingLoginMarkersIsNotALoginWall(t *testing.T) {
	text := `use @login_required and {\"require_login\": true} in Django`
	s := newSite(t)
	s.raw(webHost+"/api/v1/users/web_profile_info/", 200,
		`{"data":{"user":{"id":"1001","username":"alice","biography":"`+text+`","edge_followed_by":{"count":1},"edge_follow":{"count":1},"edge_owner_to_timeline_media":{"count":1}}}}`)
	s.raw(mobileHost+"/api/v1/feed/user/1001/", 200,
		`{"items":[{"code":"X1","taken_at":5,"caption":{"text":"`+text+`"},"user":{}}],"more_available":false}`)
	res, err := scrape(s.module("", nil), profile(), 5, 0)
	require.NoError(t, err)
	assert.Contains(t, *res.Bio, "login_required")
	require.Len(t, res.Posts, 1)
	assert.Contains(t, res.Posts[0].Text, "login_required")
	assert.Empty(t, res.Warnings)
}
