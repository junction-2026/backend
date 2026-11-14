// Package facebook reads public Facebook pages with headless chromium (chromedp) and parses the GraphQL
// responses the page itself requests. No login, public pages only.
//
// Assumed GraphQL node shapes (the walker looks for these node types anywhere in a response):
//
//	profile  {"__typename": "Page"|"User", "id", "name", "url"|"vanity", "followers_count", "description", "is_verified"}
//	post     {"__typename": "Story", "post_id"|"id", "owner": {"id"}, "message": {"text"}, "creation_time", "url",
//	          "feedback": {"reaction_count": {"count"}, "comment_count": {"total_count"}, "share_count": {"count"}}}
//	comment  {"__typename": "Comment", "id", "body": {"text"}, "author": {"id", "name", "url"}, "created_time",
//	          "comment_parent": {"id"}, "feedback": {...}}
//
// A "User" profile node means a personal profile, which is never scraped.
package facebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const (
	platform  = ss.Facebook
	budget    = 0.8 // share of the scrape deadline the scrolling may use; the rest is for parsing and cleanup
	maxRounds = 12  // scroll rounds for history
)

var unavailable = []string{"isn't available", "isn’t available", "may have been removed"}

// Browser is one headless browser session. Close must be safe to call once on every path.
type Browser interface {
	// Navigate opens url and returns the HTTP status of the main document.
	Navigate(url string) (status int, err error)
	// Scroll loads more of the page and waits briefly for the page's own requests.
	Scroll() error
	// Bodies returns the bodies of the page's /api/graphql/ responses seen so far.
	Bodies() [][]byte
	// Text returns the visible text of the page ("" when it cannot be read).
	Text() string
	Close()
}

// Module is the Facebook platform module. Launch starts a browser bound to ctx.
type Module struct {
	Launch func(ctx context.Context) (Browser, error)
}

// New returns the module over the real chromedp launcher.
func New() *Module { return &Module{Launch: launchChrome} }

func (m *Module) Scrape(ctx context.Context, t ss.Target, req ss.Request) (*ss.Result, error) {
	page := t.IDs["page"]
	path := page
	if t.URLKind == ss.KindPost {
		path = page + "/posts/" + t.IDs["post_id"]
	}
	b, err := m.Launch(ctx)
	if err != nil {
		return nil, navError(ctx, err)
	}
	defer b.Close() // every path: success, error, panic, cancellation

	status, err := b.Navigate("https://www.facebook.com/" + path)
	if err != nil {
		return nil, navError(ctx, err)
	}
	var text string
	var bodies [][]byte
	if status == 404 {
		text = unavailable[0]
	} else {
		if err := scroll(ctx, b, t.URLKind, req); err != nil {
			return nil, err
		}
		bodies, text = b.Bodies(), b.Text()
	}
	return build(t, req, parseNodes(bodies), text)
}

// navError maps a launch or navigation failure; the upstream text is never copied.
func navError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		if ss.IsTimeout(ctx.Err()) {
			return ss.UpstreamTimeout(platform)
		}
		return ctx.Err()
	}
	if ss.IsTimeout(err) {
		return ss.UpstreamTimeout(platform)
	}
	slog.Warn("facebook browser failure", "error_type", typeName(err))
	return ss.UpstreamError(platform, "Facebook could not be read with the browser")
}

// scroll loads history: up to maxRounds for a profile, two for a post (its first comments load by themselves).
func scroll(ctx context.Context, b Browser, kind string, req ss.Request) error {
	end := time.Time{}
	if d, ok := ctx.Deadline(); ok {
		end = time.Now().Add(time.Duration(float64(time.Until(d)) * budget))
	}
	rounds := 2
	if kind == ss.KindProfile {
		rounds = maxRounds
	}
	seen := -1
	for i := 0; i < rounds; i++ {
		if ctx.Err() != nil {
			return navError(ctx, ctx.Err())
		}
		have := len(byID(parseNodes(b.Bodies()), "Story").order)
		if (!end.IsZero() && time.Now().After(end)) || (kind == ss.KindProfile && have >= req.MaxPosts) || have == seen {
			return nil
		}
		seen = have
		if err := b.Scroll(); err != nil {
			return navError(ctx, err)
		}
	}
	return nil
}

type node = map[string]any

// parseNodes decodes every JSON document of every body (a body is one JSON value or one document per line,
// optionally behind a "for (;;);" guard) and returns all objects at any depth, in document order.
func parseNodes(bodies [][]byte) []node {
	var out []node
	for _, body := range bodies {
		body = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(body), []byte("for (;;);")))
		var docs []any
		var whole any
		if json.Unmarshal(body, &whole) == nil {
			docs = []any{whole}
		} else {
			for _, line := range bytes.Split(body, []byte("\n")) {
				var d any
				if json.Unmarshal(bytes.TrimSpace(line), &d) == nil {
					docs = append(docs, d)
				}
			}
		}
		for _, d := range docs {
			walk(d, func(n node) { out = append(out, n) })
		}
	}
	return out
}

func walk(o any, fn func(node)) {
	switch v := o.(type) {
	case map[string]any:
		fn(v)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic order
		for _, k := range keys {
			walk(v[k], fn)
		}
	case []any:
		for _, e := range v {
			walk(e, fn)
		}
	}
}

// first returns the first non-nil value at one of the dotted paths.
func first(n node, paths ...string) any {
	for _, p := range paths {
		var v any = n
		for _, k := range strings.Split(p, ".") {
			m, ok := v.(map[string]any)
			if !ok {
				v = nil
				break
			}
			v = m[k]
		}
		if v != nil {
			return v
		}
	}
	return nil
}

func str(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return numString(s)
	}
	return ""
}

func numString(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e18 {
		return strconv.FormatInt(int64(f), 10)
	}
	return ""
}

func typeName(err error) string { return fmt.Sprintf("%T", err) }

func intOf(v any) *int {
	if f, ok := v.(float64); ok && f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return ss.Ptr(int(f))
	}
	return nil
}

func ts(v any) *string {
	f, ok := v.(float64)
	if !ok || math.Abs(f) > 1e12 {
		return nil
	}
	return ss.Ptr(time.Unix(int64(f), 0).UTC().Format("2006-01-02T15:04:05Z"))
}

func slug(url any) string {
	s := str(url)
	s, _, _ = strings.Cut(s, "?")
	s = strings.TrimRight(s, "/")
	return strings.ToLower(s[strings.LastIndex(s, "/")+1:])
}

func typename(n node) string { return str(n["__typename"]) }

// profileNode prefers the node that names this page; else the first Page node (a post page also carries
// User nodes of its commenters).
func profileNode(nodes []node, page string) node {
	var named []node
	for _, n := range nodes {
		if tn := typename(n); (tn == "Page" || tn == "User") && str(n["name"]) != "" {
			named = append(named, n)
		}
	}
	page = strings.ToLower(page)
	for _, n := range named {
		if page == slug(n["url"]) || page == strings.ToLower(str(n["vanity"])) {
			return n
		}
	}
	for _, n := range named {
		if typename(n) == "Page" {
			return n
		}
	}
	return nil
}

// idSet keeps the first node of each id in the order of first appearance.
type idSet struct {
	order []string
	m     map[string]node
}

func byID(nodes []node, tn string) idSet {
	s := idSet{m: map[string]node{}}
	for _, n := range nodes {
		if typename(n) != tn {
			continue
		}
		id := str(n["post_id"])
		if id == "" {
			id = str(n["id"])
		}
		if id == "" {
			continue
		}
		if _, ok := s.m[id]; !ok {
			s.m[id] = n
			s.order = append(s.order, id)
		}
	}
	return s
}

func post(n node) ss.Post {
	id := str(n["post_id"])
	if id == "" {
		id = str(n["id"])
	}
	url := str(n["url"])
	if url == "" {
		url = str(n["wwwURL"])
	}
	return ss.Post{ID: id, Text: strings.TrimSpace(str(first(n, "message.text"))), CreatedAt: ts(n["creation_time"]),
		URL: ss.NonEmpty(url), Likes: intOf(first(n, "feedback.reaction_count.count")),
		Replies: intOf(first(n, "feedback.comment_count.total_count")), Reposts: intOf(first(n, "feedback.share_count.count"))}
}

func comment(n node, postID string) ss.Comment {
	a, _ := n["author"].(map[string]any)
	user := slug(a["url"])
	if user == "" {
		user = str(a["id"])
	}
	parent := str(first(n, "comment_parent.id"))
	if parent == "" {
		parent = postID
	}
	return ss.Comment{
		Post: ss.Post{ID: str(n["id"]), Text: strings.TrimSpace(str(first(n, "body.text"))), CreatedAt: ts(n["created_time"]),
			Likes: intOf(first(n, "feedback.reaction_count.count")), Replies: intOf(first(n, "feedback.reply_count.count"))},
		Author:      ss.Author{Username: user, DisplayName: ss.NonEmpty(str(a["name"]))},
		InReplyToID: &parent,
	}
}

func build(t ss.Target, req ss.Request, nodes []node, text string) (*ss.Result, error) {
	page, kind := t.IDs["page"], t.URLKind
	prof := profileNode(nodes, page)
	if prof != nil && typename(prof) == "User" {
		return nil, ss.UnsupportedAccountType(platform, "personal_profile", "Facebook personal profiles are not scraped")
	}
	stories := byID(nodes, "Story")
	var target node
	if kind == ss.KindPost {
		pid := t.IDs["post_id"]
		target = stories.m[pid]
		for _, id := range stories.order {
			if target == nil && strings.HasSuffix(strings.TrimRight(str(stories.m[id]["url"]), "/"), pid) {
				target = stories.m[id]
			}
		}
	}
	if prof == nil {
		lower := strings.ToLower(text)
		for _, m := range unavailable {
			if strings.Contains(lower, m) {
				if kind == ss.KindPost {
					return nil, ss.PostNotFound(platform, "the Facebook post does not exist or is not public")
				}
				return nil, ss.AccountNotFound(platform, "the Facebook page does not exist or is not public")
			}
		}
		return nil, ss.UpstreamError(platform, "Facebook returned no recognizable public page data")
	}
	if kind == ss.KindPost && target == nil {
		return nil, ss.PostNotFound(platform, "the Facebook post does not exist or is not public")
	}

	var warnings []ss.Warning
	followers := intOf(first(prof, "followers_count", "follower_count"))
	if followers == nil {
		warnings = append(warnings, ss.Warning{Code: "partial_parse", Message: "the follower count was not in the page data"})
	}
	// a Story with an "owner" belongs to that profile only; related stories on a post page are dropped
	pid := str(prof["id"]) // without a profile id the owner filter cannot apply
	var posts []ss.Post
	for _, id := range stories.order {
		s := stories.m[id]
		if owner := str(first(s, "owner.id")); pid == "" || owner == "" || owner == pid {
			posts = append(posts, post(s))
		}
	}
	sort.SliceStable(posts, func(i, j int) bool { return deref(posts[i].CreatedAt) > deref(posts[j].CreatedAt) })
	if len(posts) > req.MaxPosts {
		posts = posts[:req.MaxPosts]
	}
	if kind == ss.KindProfile && len(posts) < req.MaxPosts {
		warnings = append(warnings, ss.Warning{Code: "history_truncated", Message: "fewer posts than requested were loaded"})
	}
	res := &ss.Result{
		Platform: platform, URLKind: kind, Username: page, DisplayName: ss.NonEmpty(str(prof["name"])),
		Bio: ss.NonEmpty(strings.TrimSpace(str(prof["description"]))), ProfileURL: ss.Ptr("https://www.facebook.com/" + page),
		Followers: followers, Posts: posts,
	}
	if v, ok := prof["is_verified"].(bool); ok {
		res.Verified = &v
	}
	if target != nil {
		p := post(target)
		res.Post = &p
		if req.MaxComments > 0 {
			found := byID(nodes, "Comment")
			for _, id := range found.order {
				res.Comments = append(res.Comments, comment(found.m[id], p.ID))
			}
			sort.SliceStable(res.Comments, func(i, j int) bool { return deref(res.Comments[i].CreatedAt) < deref(res.Comments[j].CreatedAt) })
			if len(res.Comments) > req.MaxComments {
				res.Comments = res.Comments[:req.MaxComments]
			}
			if len(res.Comments) == 0 {
				warnings = append(warnings, ss.Warning{Code: "comments_unavailable", Message: "no comments were loaded for the post"})
			}
		}
	}
	res.Warnings = warnings
	return res, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
