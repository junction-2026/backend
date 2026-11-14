// Package bluesky scrapes Bluesky through direct XRPC calls to the public AppView (no SDK, no authentication).
package bluesky

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const (
	AppView  = "https://public.api.bsky.app"
	platform = ss.Bluesky
	pageSize = 100 // getAuthorFeed maximum
	// shortcut: at most 1000 feed items are inspected per scrape; raise it if reposts crowd out history.
	maxPages = 10
)

// Module is the Bluesky platform module.
type Module struct{ HTTP *http.Client }

// New returns the module over hc (ss.NewClient in production, a scrapertest router client in tests).
func New(hc *http.Client) *Module { return &Module{HTTP: hc} }

type profile struct {
	DID            string  `json:"did"`
	Handle         string  `json:"handle"`
	DisplayName    string  `json:"displayName"`
	Description    string  `json:"description"`
	FollowersCount *int    `json:"followersCount"`
	FollowsCount   *int    `json:"followsCount"`
	PostsCount     *int    `json:"postsCount"`
	Labels         []label `json:"labels"`
	Verification   *struct {
		VerifiedStatus string `json:"verifiedStatus"`
	} `json:"verification"`
}

type label struct {
	Val string `json:"val"`
}

type author struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}

type postView struct {
	URI    string `json:"uri"`
	Author author `json:"author"`
	Record struct {
		Text      string `json:"text"`
		CreatedAt string `json:"createdAt"`
	} `json:"record"`
	IndexedAt   string `json:"indexedAt"`
	LikeCount   *int   `json:"likeCount"`
	ReplyCount  *int   `json:"replyCount"`
	RepostCount *int   `json:"repostCount"`
}

type feedPage struct {
	Cursor string `json:"cursor"`
	Feed   []struct {
		Post   postView        `json:"post"`
		Reason json.RawMessage `json:"reason"`
	} `json:"feed"`
}

type threadNode struct {
	Type    string             `json:"$type"`
	Post    *postView          `json:"post"`
	Replies *[]json.RawMessage `json:"replies"` // nil: the AppView sent no replies member
}

func (n threadNode) viewable() bool {
	return n.Post != nil && (n.Type == "" || strings.HasSuffix(n.Type, "#threadViewPost"))
}

func (p postView) toPost() (ss.Post, error) {
	if p.URI == "" || p.Author.Handle == "" {
		return ss.Post{}, errors.New("post without uri or author")
	}
	created := p.Record.CreatedAt
	if created == "" {
		created = p.IndexedAt
	}
	return ss.Post{
		ID: p.URI, Text: strings.TrimSpace(p.Record.Text), CreatedAt: ss.Timestamp(created),
		URL:   ss.Ptr("https://bsky.app/profile/" + p.Author.Handle + "/post/" + p.URI[strings.LastIndex(p.URI, "/")+1:]),
		Likes: p.LikeCount, Replies: p.ReplyCount, Reposts: p.RepostCount,
	}, nil
}

// get calls one XRPC query. what is "account" or "post": the thing a 400 says is missing.
func (m *Module) get(ctx context.Context, method string, q url.Values, what string, out any) *ss.Error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, AppView+"/xrpc/"+method+"?"+q.Encode(), nil)
	if err != nil {
		return ss.UpstreamError(platform, "could not build the Bluesky request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "junction-scraper/1")
	resp, err := m.HTTP.Do(req)
	if err != nil {
		if ss.IsTimeout(err) {
			return ss.UpstreamTimeout(platform)
		}
		return ss.UpstreamError(platform, "could not reach Bluesky")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return ss.RateLimited(platform, retryAfter(resp.Header))
	case resp.StatusCode == http.StatusBadRequest:
		return notFound(what)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return ss.UpstreamError(platform, fmt.Sprintf("Bluesky answered with an error (%d)", resp.StatusCode))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ss.IsTimeout(err) {
			return ss.UpstreamTimeout(platform)
		}
		return ss.UpstreamError(platform, "could not read the Bluesky answer")
	}
	if json.Unmarshal(body, out) != nil {
		return ss.UpstreamError(platform, "Bluesky answered with data that could not be parsed")
	}
	return nil
}

func notFound(what string) *ss.Error {
	if what == "post" {
		return ss.PostNotFound(platform, "the Bluesky post does not exist or was deleted")
	}
	return ss.AccountNotFound(platform, "the Bluesky account does not exist or was deleted")
}

// retryAfter reads Retry-After (seconds) or ratelimit-reset (Unix time); -1 when unknown.
func retryAfter(h http.Header) int {
	if n, err := strconv.Atoi(h.Get("Retry-After")); err == nil && n >= 0 {
		return n
	}
	if reset, err := strconv.ParseInt(h.Get("Ratelimit-Reset"), 10, 64); err == nil {
		return max(0, int(time.Until(time.Unix(reset, 0)).Seconds()))
	}
	return -1
}

// Scrape implements socialscraper.Module.
func (m *Module) Scrape(ctx context.Context, t ss.Target, req ss.Request) (*ss.Result, error) {
	var p profile
	if err := m.get(ctx, "app.bsky.actor.getProfile", url.Values{"actor": {t.IDs["actor"]}}, "account", &p); err != nil {
		return nil, err
	}
	if p.DID == "" || p.Handle == "" {
		return nil, ss.UpstreamError(platform, "Bluesky answered with data that could not be parsed")
	}
	for _, l := range p.Labels {
		if l.Val == "!no-unauthenticated" {
			return nil, ss.AccountPrivate(platform, "the Bluesky account is not visible without login")
		}
	}
	res := &ss.Result{
		Platform: platform, URLKind: t.URLKind, Username: p.Handle,
		DisplayName: ss.NonEmpty(p.DisplayName), Bio: ss.Ptr(strings.TrimSpace(p.Description)),
		ProfileURL: ss.Ptr("https://bsky.app/profile/" + p.Handle),
		Followers:  p.FollowersCount, Following: p.FollowsCount, PostCount: p.PostsCount,
	}
	if p.Verification != nil {
		res.Verified = ss.Ptr(p.Verification.VerifiedStatus == "valid")
	}
	res.Posts, res.Warnings = m.history(ctx, p.DID, req.MaxPosts)
	if t.URLKind == ss.KindPost {
		if err := m.thread(ctx, p.DID, t.IDs["rkey"], req, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// history follows the author feed cursor (own posts only, reposts skipped). A failure becomes a warning.
func (m *Module) history(ctx context.Context, did string, limit int) (posts []ss.Post, warnings []ss.Warning) {
	cursor := ""
	for range maxPages {
		q := url.Values{"actor": {did}, "filter": {"posts_no_replies"}, "limit": {strconv.Itoa(pageSize)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page feedPage
		err := m.get(ctx, "app.bsky.feed.getAuthorFeed", q, "account", &page)
		if err == nil {
			for _, item := range page.Feed {
				if len(item.Reason) > 0 && string(item.Reason) != "null" { // a repost
					continue
				}
				p, perr := item.Post.toPost()
				if perr != nil {
					err = ss.UpstreamError(platform, "Bluesky answered with data that could not be parsed")
					break
				}
				posts = append(posts, p)
			}
		}
		if err != nil {
			code := "history_unavailable"
			if len(posts) > 0 {
				code = "history_truncated"
			}
			warnings = append(warnings, ss.Warning{Code: code, Message: err.Message})
			break
		}
		cursor = page.Cursor
		if len(posts) >= limit || cursor == "" || len(page.Feed) == 0 {
			break
		}
	}
	if len(posts) > limit {
		posts = posts[:limit]
	}
	return posts, warnings
}

func (m *Module) thread(ctx context.Context, did, rkey string, req ss.Request, res *ss.Result) *ss.Error {
	var out struct {
		Thread threadNode `json:"thread"`
	}
	q := url.Values{"uri": {"at://" + did + "/app.bsky.feed.post/" + rkey}, "depth": {"10"}, "parentHeight": {"0"}}
	if err := m.get(ctx, "app.bsky.feed.getPostThread", q, "post", &out); err != nil {
		return err
	}
	if !out.Thread.viewable() {
		return ss.PostNotFound(platform, "the Bluesky post does not exist or is not visible")
	}
	post, err := out.Thread.Post.toPost()
	if err != nil {
		return ss.UpstreamError(platform, "Bluesky answered with data that could not be parsed")
	}
	res.Post = &post
	if req.MaxComments == 0 {
		return nil
	}
	var comments []ss.Comment
	flatten(out.Thread, post.ID, &comments)
	if out.Thread.Replies == nil && out.Thread.Post.ReplyCount != nil && *out.Thread.Post.ReplyCount > 0 {
		res.Warnings = append(res.Warnings, ss.Warning{Code: "comments_unavailable", Message: "Bluesky returned no replies"})
	}
	sort.SliceStable(comments, func(i, j int) bool { return deref(comments[i].CreatedAt) < deref(comments[j].CreatedAt) })
	if len(comments) > req.MaxComments {
		comments = comments[:req.MaxComments]
	}
	res.Comments = comments
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// flatten appends the viewable replies below n (depth first); deleted and blocked replies are skipped.
func flatten(n threadNode, parentURI string, out *[]ss.Comment) {
	if n.Replies == nil {
		return
	}
	for _, raw := range *n.Replies {
		var r threadNode
		if json.Unmarshal(raw, &r) != nil || !r.viewable() {
			continue
		}
		p, err := r.Post.toPost()
		if err != nil {
			continue
		}
		*out = append(*out, ss.Comment{
			Post:        p,
			Author:      ss.Author{Username: r.Post.Author.Handle, DisplayName: ss.NonEmpty(r.Post.Author.DisplayName)},
			InReplyToID: ss.Ptr(parentURI),
		})
		flatten(r, p.ID, out)
	}
}
