package facebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/scrapertest"
)

const page = "https://www.facebook.com/examplenews"

// fake is a scripted Browser: batches are emitted on Navigate, then one more per Scroll.
type fake struct {
	ctx       context.Context
	batches   [][]byte
	status    int
	text      string
	navErr    error
	navBlock  bool // Navigate waits for the context, like a page that never loads
	panicNav  string
	url       string
	scrolls   int
	bodies    [][]byte
	closed    atomic.Int32
	launchErr error
}

// batch turns a fixture (a JSON array of GraphQL documents) into one body with one document per line.
func batch(t testing.TB, name string) []byte { return batchOf(t, scrapertest.Fixture(t, name)) }

func batchOf(t testing.TB, raw []byte) []byte {
	var docs []json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &docs))
	var out []byte
	for _, d := range docs {
		var line bytes.Buffer
		require.NoError(t, json.Compact(&line, d)) // fixtures are indented; a body carries one document per line
		out = append(append(out, line.Bytes()...), '\n')
	}
	return out
}

func (f *fake) emit() {
	if len(f.batches) > 0 {
		f.bodies = append(f.bodies, f.batches[0])
		f.batches = f.batches[1:]
	}
}

func (f *fake) Navigate(url string) (int, error) {
	f.url = url
	if f.panicNav != "" {
		panic(f.panicNav)
	}
	if f.navBlock {
		<-f.ctx.Done()
		return 0, f.ctx.Err()
	}
	if f.navErr != nil {
		return 0, f.navErr
	}
	f.emit()
	return f.status, nil
}
func (f *fake) Scroll() error    { f.scrolls++; f.emit(); return nil }
func (f *fake) Bodies() [][]byte { return f.bodies }
func (f *fake) Text() string     { return f.text }
func (f *fake) Close()           { f.closed.Add(1) }

func module(f *fake) *Module {
	return &Module{Launch: func(ctx context.Context) (Browser, error) {
		f.ctx = ctx
		if f.launchErr != nil {
			return nil, f.launchErr
		}
		return f, nil
	}}
}

func newFake(t testing.TB, fixtures ...string) *fake {
	f := &fake{status: 200}
	for _, n := range fixtures {
		f.batches = append(f.batches, batch(t, n))
	}
	return f
}

func target(kind string, ids map[string]string) ss.Target {
	return ss.Target{Platform: "facebook", URLKind: kind, IDs: ids}
}

func profile() ss.Target { return target("profile", map[string]string{"page": "examplenews"}) }
func postT(id string) ss.Target {
	return target("post", map[string]string{"page": "examplenews", "post_id": id})
}

func run(f *fake, t ss.Target, posts, comments int) (*ss.Result, error) {
	return module(f).Scrape(context.Background(), t, ss.Request{MaxPosts: posts, MaxComments: comments})
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

func TestPageProfileWithScrolledHistory(t *testing.T) {
	f := newFake(t, "page_graphql_1.json", "page_graphql_2.json")
	r, err := run(f, profile(), 3, 5)
	require.NoError(t, err)
	assert.Equal(t, "facebook", r.Platform)
	assert.Equal(t, "profile", r.URLKind)
	assert.Equal(t, "examplenews", r.Username)
	assert.Equal(t, "Example News", *r.DisplayName)
	assert.Equal(t, "Local news & weather", *r.Bio)
	assert.Equal(t, 45210, *r.Followers)
	assert.True(t, *r.Verified)
	assert.Nil(t, r.Following)
	assert.Equal(t, page, *r.ProfileURL)
	var ids []string
	for _, p := range r.Posts {
		ids = append(ids, p.ID)
	}
	assert.Equal(t, []string{"9003", "9002", "9001"}, ids) // newest first; the other owner's story is dropped
	p := r.Posts[0]
	assert.Equal(t, "Newest story", p.Text)
	assert.Equal(t, "2026-11-14T09:30:15Z", *p.CreatedAt)
	assert.Equal(t, page+"/posts/9003", *p.URL)
	assert.Equal(t, []int{40, 7, 3}, []int{*p.Likes, *p.Replies, *p.Reposts})
	assert.Nil(t, r.Posts[1].Replies)
	assert.Nil(t, r.Post)
	assert.Empty(t, r.Warnings)
	assert.Equal(t, 1, f.scrolls) // one scroll loaded the second batch, then there were enough
	assert.Equal(t, page, f.url)
	assert.EqualValues(t, 1, f.closed.Load())
}

func TestProfileWithoutEnoughPostsWarns(t *testing.T) {
	f := newFake(t, "page_graphql_1.json")
	r, err := run(f, profile(), 10, 5)
	require.NoError(t, err)
	assert.Len(t, r.Posts, 2)
	assert.Equal(t, []string{"history_truncated"}, codes(r))
	assert.EqualValues(t, 1, f.closed.Load())
}

func TestPagePostWithTwoComments(t *testing.T) {
	f := newFake(t, "post_graphql.json")
	r, err := run(f, postT("9003"), 20, 5)
	require.NoError(t, err)
	assert.Equal(t, "post", r.URLKind)
	assert.Equal(t, "9003", r.Post.ID)
	assert.Equal(t, "Newest story", r.Post.Text)
	assert.Equal(t, 45210, *r.Followers)
	assert.Nil(t, r.Verified)
	assert.Nil(t, r.Bio)
	require.Len(t, r.Comments, 2)
	c1, c2 := r.Comments[0], r.Comments[1]
	assert.Equal(t, "c1", c1.ID) // oldest first although served newest first
	assert.Equal(t, ss.Author{Username: "300", DisplayName: ss.Ptr("Eli")}, c1.Author)
	assert.Equal(t, "9003", *c1.InReplyToID)
	assert.Equal(t, 2, *c1.Likes)
	assert.Equal(t, "dana.k", c2.Author.Username)
	assert.Equal(t, "c1", *c2.InReplyToID)
	assert.Len(t, r.Posts, 1)
	assert.Equal(t, page+"/posts/9003", f.url) // rebuilt, never the client's host or query
	assert.EqualValues(t, 1, f.closed.Load())
}

func TestCommentsCappedOrSkipped(t *testing.T) {
	r, err := run(newFake(t, "post_graphql.json"), postT("9003"), 20, 1)
	require.NoError(t, err)
	assert.Len(t, r.Comments, 1)
	r, err = run(newFake(t, "post_graphql.json"), postT("9003"), 20, 0)
	require.NoError(t, err)
	assert.Empty(t, r.Comments)
	assert.Empty(t, r.Warnings)
}

func TestPostWithoutCommentsWarns(t *testing.T) {
	var docs []json.RawMessage
	require.NoError(t, json.Unmarshal(scrapertest.Fixture(t, "post_graphql.json"), &docs))
	body := batchOf(t, mustJSON(t, docs[:2])) // page and story, no comments document
	f := &fake{status: 200, batches: [][]byte{body}}
	r, err := run(f, postT("9003"), 20, 5)
	require.NoError(t, err)
	assert.Equal(t, "9003", r.Post.ID)
	assert.Equal(t, []string{"comments_unavailable"}, codes(r))
}

func mustJSON(t testing.TB, v any) []byte {
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func TestMissingFollowerCountIsPartialParse(t *testing.T) {
	var docs []map[string]any
	require.NoError(t, json.Unmarshal(scrapertest.Fixture(t, "page_graphql_1.json"), &docs))
	delete(docs[0]["data"].(map[string]any)["page"].(map[string]any), "followers_count")
	f := &fake{status: 200, batches: [][]byte{batchOf(t, mustJSON(t, docs))}}
	r, err := run(f, profile(), 2, 5)
	require.NoError(t, err)
	assert.Nil(t, r.Followers)
	assert.Contains(t, codes(r), "partial_parse")
}

func TestPersonalProfileByVanityURL(t *testing.T) {
	f := newFake(t, "personal_graphql.json")
	_, err := run(f, target("profile", map[string]string{"page": "jane.doe"}), 20, 5)
	e := typed(t, err)
	assert.Equal(t, "unsupported_account_type", e.Code)
	assert.Equal(t, 422, e.Status)
	assert.Equal(t, map[string]any{"platform": "facebook", "account_type": "personal_profile"}, e.Details)
	assert.EqualValues(t, 1, f.closed.Load())
}

func TestCommenterUserNodesAreNotTheProfile(t *testing.T) {
	_, err := run(newFake(t, "post_graphql.json"), postT("9003"), 20, 5) // Page node wins over the commenters' User nodes
	assert.NoError(t, err)
}

func TestUnavailableTextWinsOverUnrelatedGraphQL(t *testing.T) {
	other := []byte(`{"data":{"viewer":{"__typename":"User","id":"1"}}}` + "\n")
	for kind, want := range map[string]string{"profile": "account_not_found", "post": "post_not_found"} {
		f := &fake{status: 200, batches: [][]byte{other}, text: "This content isn't available right now"}
		tg := profile()
		if kind == "post" {
			tg = postT("1")
		}
		_, err := run(f, tg, 20, 5)
		assert.Equal(t, want, typed(t, err).Code)
		assert.Equal(t, "facebook", typed(t, err).Details["platform"])
		assert.EqualValues(t, 1, f.closed.Load())
	}
}

func TestUnavailablePages(t *testing.T) {
	tests := []struct {
		name   string
		t      ss.Target
		status int
		text   string
		code   string
		http   int
	}{
		{"404 profile", profile(), 404, "", "account_not_found", 404},
		{"404 post", postT("1"), 404, "", "post_not_found", 404},
		{"login wall", profile(), 200, "Log in to continue", "upstream_error", 502},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{status: tc.status, text: tc.text}
			_, err := run(f, tc.t, 20, 5)
			e := typed(t, err)
			assert.Equal(t, tc.code, e.Code)
			assert.Equal(t, tc.http, e.Status)
			assert.Equal(t, "facebook", e.Details["platform"])
			assert.EqualValues(t, 1, f.closed.Load())
		})
	}
}

func TestPostMissingFromPageIsNotFound(t *testing.T) {
	f := newFake(t, "post_graphql.json") // the page loads, but names another post
	_, err := run(f, postT("424242"), 20, 5)
	assert.Equal(t, "post_not_found", typed(t, err).Code)
	assert.EqualValues(t, 1, f.closed.Load())
}

func TestPageNodeWithoutIDStillScrapes(t *testing.T) {
	var docs []map[string]any
	require.NoError(t, json.Unmarshal(scrapertest.Fixture(t, "page_graphql_1.json"), &docs))
	delete(docs[0]["data"].(map[string]any)["page"].(map[string]any), "id")
	r, err := run(&fake{status: 200, batches: [][]byte{batchOf(t, mustJSON(t, docs))}}, profile(), 2, 5)
	require.NoError(t, err)
	assert.Len(t, r.Posts, 2)
}

func TestBodyFormats(t *testing.T) {
	// one JSON value, and the "for (;;);" guard, both parse like documents per line
	for _, body := range []string{
		`for (;;);{"data":{"page":{"__typename":"Page","name":"Example News","url":"https://www.facebook.com/examplenews","followers_count":1}}}`,
		`{"data":{"page":{"__typename":"Page","name":"Example News","url":"https://www.facebook.com/examplenews","followers_count":1}}}`,
		"garbage\n" + `{"data":{"page":{"__typename":"Page","name":"Example News","followers_count":1}}}`,
	} {
		r, err := run(&fake{status: 200, batches: [][]byte{[]byte(body)}}, profile(), 5, 5)
		require.NoError(t, err)
		assert.Equal(t, 1, *r.Followers)
	}
}

func TestNavigationFailuresCloseTheBrowser(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
		http int
	}{
		{"deadline", context.DeadlineExceeded, "upstream_timeout", 504},
		{"other", errors.New("page load error net::ERR_CONNECTION_RESET with sentinel-secret"), "upstream_error", 502},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{navErr: tc.err}
			_, err := run(f, profile(), 20, 5)
			e := typed(t, err)
			assert.Equal(t, tc.code, e.Code)
			assert.Equal(t, tc.http, e.Status)
			assert.NotContains(t, e.Message, "sentinel-secret")
			assert.EqualValues(t, 1, f.closed.Load())
		})
	}
}

func TestLaunchFailureIsUpstreamError(t *testing.T) {
	f := &fake{launchErr: errors.New("exec: chromium not found")}
	_, err := run(f, profile(), 20, 5)
	e := typed(t, err)
	assert.Equal(t, "upstream_error", e.Code)
	assert.NotContains(t, e.Message, "chromium")
}

func TestNavigationTimeoutOnContextEnd(t *testing.T) {
	f := &fake{navBlock: true}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := module(f).Scrape(ctx, profile(), ss.Request{MaxPosts: 5, MaxComments: 5})
	e := typed(t, err)
	assert.Equal(t, "upstream_timeout", e.Code)
	assert.Equal(t, 504, e.Status)
	assert.EqualValues(t, 1, f.closed.Load())
}

func TestCancellationClosesTheBrowser(t *testing.T) {
	f := &fake{navBlock: true}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, err := module(f).Scrape(ctx, profile(), ss.Request{MaxPosts: 5, MaxComments: 5})
	assert.ErrorIs(t, err, context.Canceled)
	assert.EqualValues(t, 1, f.closed.Load())
}

func TestPanicClosesTheBrowser(t *testing.T) {
	f := &fake{panicNav: "boom"}
	assert.PanicsWithValue(t, "boom", func() { _, _ = run(f, profile(), 20, 5) })
	assert.EqualValues(t, 1, f.closed.Load())
}

// A test that reaches the real launcher without a fake must fail, not start chromium.
func TestRealLauncherRefusesUnderTest(t *testing.T) {
	assert.Panics(t, func() { _, _ = New().Scrape(context.Background(), profile(), ss.Request{MaxPosts: 1}) })
}
