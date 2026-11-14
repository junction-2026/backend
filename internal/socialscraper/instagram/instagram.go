// Package instagram scrapes public Instagram profiles and posts through the JSON endpoints of Instagram's web
// app (endpoints.go), optionally with the cookies of a session file.
package instagram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const (
	platform         = ss.Instagram
	maxFeedPages     = 10
	maxCommentPages  = 5  // shortcut: the oldest comments of a post with more pages are missed; raise when needed
	maxChildFetches  = 20 // child_comments requests per scrape
	maxFeedPageCount = 50
)

// Module is the Instagram platform module.
type Module struct {
	HTTP    *http.Client
	session *session // nil: anonymous
}

// New returns the module over a copy of hc that never follows redirects (a redirect is the login wall, and only
// the Instagram hosts are contacted). sessionFile is INSTAGRAM_SESSION_FILE ("" for none); log receives the one
// fixed warning when the file cannot be used.
func New(hc *http.Client, sessionFile string, log *slog.Logger) *Module {
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Module{HTTP: &c, session: loadSession(sessionFile, log)}
}

// id is a JSON string or number kept as text.
type id string

func (i *id) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	*i = id(strings.Trim(string(b), `"`))
	return nil
}

type count struct {
	Count *int `json:"count"`
}

type user struct {
	ID        id      `json:"id"`
	PK        id      `json:"pk"`
	Username  string  `json:"username"`
	FullName  string  `json:"full_name"`
	Biography *string `json:"biography"`
	Verified  *bool   `json:"is_verified"`
	Private   bool    `json:"is_private"`
	Followers *count  `json:"edge_followed_by"`
	Following *count  `json:"edge_follow"`
	Media     *count  `json:"edge_owner_to_timeline_media"`
}

func (u user) uid() string {
	if u.ID != "" {
		return string(u.ID)
	}
	return string(u.PK)
}

func n(c *count) *int {
	if c == nil {
		return nil
	}
	return c.Count
}

// media is an item of the feed and media info endpoints.
type media struct {
	Code    string `json:"code"`
	TakenAt *int64 `json:"taken_at"`
	User    user   `json:"user"`
	Caption *struct {
		Text string `json:"text"`
	} `json:"caption"`
	LikeCount    *int `json:"like_count"`
	CommentCount *int `json:"comment_count"`
	LikesHidden  bool `json:"like_and_view_counts_disabled"`
}

func timeOf(sec *int64) *string {
	if sec == nil {
		return nil
	}
	return ss.Ptr(time.Unix(*sec, 0).UTC().Format("2006-01-02T15:04:05Z"))
}

func (m media) post() (ss.Post, bool) {
	if m.Code == "" {
		return ss.Post{}, false
	}
	p := ss.Post{ID: m.Code, CreatedAt: timeOf(m.TakenAt), URL: ss.Ptr("https://" + webHost + "/p/" + m.Code + "/"), Replies: m.CommentCount}
	if m.Caption != nil {
		p.Text = strings.TrimSpace(m.Caption.Text)
	}
	if !m.LikesHidden { // hidden likes: like_count is a placeholder 0, not a count
		p.Likes = m.LikeCount
	}
	return p, true
}

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

// mediaID decodes a shortcode (base 64, first 11 characters) to the numeric media id.
func mediaID(shortcode string) (string, bool) {
	if len(shortcode) > 11 {
		shortcode = shortcode[:11]
	}
	if shortcode == "" {
		return "", false
	}
	var v uint64
	for _, c := range []byte(shortcode) {
		i := strings.IndexByte(alphabet, c)
		if i < 0 || v > (math.MaxInt64-uint64(i))/64 {
			return "", false
		}
		v = v*64 + uint64(i)
	}
	return strconv.FormatUint(v, 10), true
}

func badData() *ss.Error {
	return ss.UpstreamError(platform, "Instagram answered with data that could not be parsed")
}

func retryAfter(h http.Header) int {
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil && s >= 0 {
		return s
	}
	return -1
}

// loginWallOK is loginWall for a 2xx answer, which carries user text: only structured markers count, never a substring.
func loginWallOK(body []byte) bool {
	b := bytes.TrimSpace(body)
	if bytes.HasPrefix(b, []byte("<")) {
		return bytes.Contains(b, []byte("/accounts/login"))
	}
	var m struct {
		Message      string `json:"message"`
		RequireLogin bool   `json:"require_login"`
	}
	return json.Unmarshal(b, &m) == nil && (m.Message == "login_required" || m.RequireLogin)
}

// loginWall reports an error answer (status >= 400) that demands a login: Instagram's login markers, or the login page.
func loginWall(body []byte) bool {
	return bytes.Contains(body, []byte("login_required")) || bytes.Contains(body, []byte(`"require_login"`)) ||
		(bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) && bytes.Contains(body, []byte("/accounts/login")))
}

// get fetches one URL and maps failures to typed errors. what is "account" or "post" (the meaning of a 404).
func (m *Module) get(ctx context.Context, u, what string) ([]byte, *ss.Error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, ss.UpstreamError(platform, "could not build the Instagram request")
	}
	m.setHeaders(req.Header)
	resp, err := m.HTTP.Do(req)
	if err != nil {
		if ss.IsTimeout(err) {
			return nil, ss.UpstreamTimeout(platform)
		}
		return nil, ss.UpstreamError(platform, "could not reach Instagram")
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(resp.Body)
	if rerr != nil && ss.IsTimeout(rerr) {
		return nil, ss.UpstreamTimeout(platform)
	}
	code := resp.StatusCode
	switch {
	case code == http.StatusTooManyRequests:
		return nil, ss.RateLimited(platform, retryAfter(resp.Header))
	case code >= 300 && code < 400:
		if strings.Contains(resp.Header.Get("Location"), "/accounts/login") {
			return nil, ss.RateLimited(platform, -1)
		}
		return nil, ss.UpstreamError(platform, fmt.Sprintf("Instagram answered %d", code))
	case code == http.StatusUnauthorized, code != http.StatusNotFound && code >= 400 && loginWall(body):
		return nil, ss.RateLimited(platform, -1)
	case code == http.StatusNotFound:
		if what == "post" {
			return nil, ss.PostNotFound(platform, "the Instagram post does not exist or was deleted")
		}
		return nil, ss.AccountNotFound(platform, "the Instagram account does not exist or was deleted")
	case code >= 400:
		return nil, ss.UpstreamError(platform, fmt.Sprintf("Instagram answered %d", code))
	}
	if rerr != nil {
		return nil, ss.UpstreamError(platform, "could not read the Instagram answer")
	}
	if loginWallOK(body) {
		return nil, ss.RateLimited(platform, -1)
	}
	return body, nil
}

func (m *Module) getJSON(ctx context.Context, u, what string, v any) *ss.Error {
	body, err := m.get(ctx, u, what)
	if err != nil {
		return err
	}
	if json.Unmarshal(body, v) != nil {
		return badData()
	}
	return nil
}

func warn(code, msg string) ss.Warning { return ss.Warning{Code: code, Message: msg} }

// Scrape implements socialscraper.Module.
func (m *Module) Scrape(ctx context.Context, t ss.Target, req ss.Request) (*ss.Result, error) {
	username := t.IDs["username"]
	var (
		item    *media
		mediaid string
	)
	if t.URLKind == ss.KindPost {
		var ok bool
		if mediaid, ok = mediaID(t.IDs["shortcode"]); !ok {
			return nil, ss.PostNotFound(platform, "the Instagram post does not exist or was deleted")
		}
		var info struct {
			Items []media `json:"items"`
		}
		if err := m.getJSON(ctx, mediaInfoURL(mediaid), "post", &info); err != nil {
			return nil, err
		}
		if len(info.Items) == 0 {
			return nil, ss.PostNotFound(platform, "the Instagram post does not exist or was deleted")
		}
		item = &info.Items[0]
		if username = item.User.Username; username == "" {
			return nil, badData()
		}
	}
	var pr struct {
		Data struct {
			User *user `json:"user"`
		} `json:"data"`
	}
	if err := m.getJSON(ctx, profileURL(username), "account", &pr); err != nil {
		return nil, err
	}
	u := pr.Data.User
	if u == nil {
		return nil, ss.AccountNotFound(platform, "the Instagram account does not exist or was deleted")
	}
	if u.Private {
		return nil, ss.AccountPrivate(platform, "the Instagram account is private")
	}
	if u.Username == "" {
		u.Username = username
	}
	res := &ss.Result{
		Platform: platform, URLKind: t.URLKind, Username: u.Username, DisplayName: ss.NonEmpty(u.FullName),
		ProfileURL: ss.Ptr("https://" + webHost + "/" + u.Username + "/"),
		Followers:  n(u.Followers), Following: n(u.Following), PostCount: n(u.Media), Verified: u.Verified,
	}
	if u.Biography != nil {
		res.Bio = ss.Ptr(strings.TrimSpace(*u.Biography))
	}
	var missing []string
	for _, c := range []struct {
		name string
		v    *int
	}{{"followers", res.Followers}, {"following", res.Following}, {"post_count", res.PostCount}} {
		if c.v == nil {
			missing = append(missing, c.name)
		}
	}
	if len(missing) > 0 {
		res.Warnings = append(res.Warnings, warn("partial_parse", "Instagram omitted: "+strings.Join(missing, ", ")))
	}
	if item != nil {
		post, ok := item.post()
		if !ok {
			return nil, badData()
		}
		res.Post = &post
		if req.MaxComments > 0 {
			var w []ss.Warning
			res.Comments, w = m.comments(ctx, mediaid, req.MaxComments)
			res.Warnings = append(res.Warnings, w...)
		}
	}
	var w []ss.Warning
	res.Posts, w = m.history(ctx, u.uid(), req.MaxPosts)
	res.Warnings = append(res.Warnings, w...)
	return res, nil
}

// history reads the feed newest first until limit posts are read; a failure keeps what was read.
// shortcut: a pinned post is listed first by Instagram and may displace one newer post at the limit.
func (m *Module) history(ctx context.Context, userID string, limit int) (posts []ss.Post, warnings []ss.Warning) {
	cursor, bad, incomplete := "", 0, false
	for range maxFeedPages {
		var page struct {
			Items     []media `json:"items"`
			More      bool    `json:"more_available"`
			NextMaxID id      `json:"next_max_id"`
		}
		var err *ss.Error
		if userID == "" {
			err = badData()
		} else {
			err = m.getJSON(ctx, feedURL(userID, min(limit, maxFeedPageCount), cursor), "account", &page)
		}
		if err != nil {
			code := "history_unavailable"
			if len(posts) > 0 {
				code = "history_truncated"
			}
			warnings = append(warnings, warn(code, err.Message))
			break
		}
		for _, it := range page.Items {
			if p, ok := it.post(); ok {
				posts = append(posts, p)
			} else {
				bad++
			}
		}
		cursor = string(page.NextMaxID)
		if len(posts) >= limit || !page.More || len(page.Items) == 0 {
			break
		}
		if cursor == "" {
			incomplete = true
			break
		}
	}
	if incomplete {
		warnings = append(warnings, warn("history_truncated", "Instagram offered no further pages"))
	}
	if bad > 0 {
		warnings = append(warnings, warn("partial_parse", fmt.Sprintf("%d post(s) could not be parsed", bad)))
	}
	sort.SliceStable(posts, func(i, j int) bool { return deref(posts[i].CreatedAt) > deref(posts[j].CreatedAt) })
	if len(posts) > limit {
		posts = posts[:limit]
	}
	return posts, warnings
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type comment struct {
	PK        id     `json:"pk"`
	Text      string `json:"text"`
	CreatedAt *int64 `json:"created_at"`
	UTC       *int64 `json:"created_at_utc"`
	User      struct {
		Username string `json:"username"`
		FullName string `json:"full_name"`
	} `json:"user"`
	Likes      *int      `json:"comment_like_count"`
	ChildCount int       `json:"child_comment_count"`
	Preview    []comment `json:"preview_child_comments"`
}

func (c comment) toComment(parent string) ss.Comment {
	at := c.CreatedAt
	if at == nil {
		at = c.UTC
	}
	out := ss.Comment{
		Post:   ss.Post{ID: string(c.PK), Text: strings.TrimSpace(c.Text), CreatedAt: timeOf(at), Likes: c.Likes},
		Author: ss.Author{Username: c.User.Username, DisplayName: ss.NonEmpty(c.User.FullName)},
	}
	if parent != "" {
		out.InReplyToID = &parent
	}
	return out
}

// comments reads a post's comments (a session is needed), oldest first, at most limit. A failure keeps what was read.
func (m *Module) comments(ctx context.Context, mediaID string, limit int) (out []ss.Comment, warnings []ss.Warning) {
	if m.session == nil {
		return nil, []ss.Warning{warn("comments_unavailable", "Instagram serves the comments only to a logged-in session")}
	}
	fail := func(err *ss.Error) {
		warnings = append(warnings, warn("comments_unavailable", err.Message))
	}
	cursorKey, cursor, children := "min_id", "", 0
read:
	for range maxCommentPages {
		var page struct {
			Comments  []comment `json:"comments"`
			More      bool      `json:"has_more_comments"`
			NextMinID id        `json:"next_min_id"`
			NextMaxID id        `json:"next_max_id"`
		}
		if err := m.getJSON(ctx, commentsURL(mediaID, cursorKey, cursor), "post", &page); err != nil {
			fail(err)
			break
		}
		for _, c := range page.Comments {
			out = append(out, c.toComment(""))
			replies := c.Preview
			if c.ChildCount > len(replies) && children < maxChildFetches {
				children++
				var cc struct {
					Children []comment `json:"child_comments"`
				}
				if err := m.getJSON(ctx, childCommentsURL(mediaID, string(c.PK)), "post", &cc); err != nil {
					fail(err)
					break read
				}
				replies = cc.Children
			}
			for _, r := range replies {
				out = append(out, r.toComment(string(c.PK)))
			}
		}
		switch {
		case !page.More:
			break read
		case page.NextMinID != "":
			cursorKey, cursor = "min_id", string(page.NextMinID)
		case page.NextMaxID != "":
			cursorKey, cursor = "max_id", string(page.NextMaxID)
		default:
			break read
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return deref(out[i].CreatedAt) < deref(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, warnings
}
