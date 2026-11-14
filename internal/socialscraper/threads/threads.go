// Package threads scrapes public Threads profiles and posts from the JSON the page embeds for its own rendering.
//
// The page embeds Relay query results in <script type="application/json"> blocks. Their nesting changes often, so
// nothing here follows a fixed path: the blocks are walked for objects that look like a user (a `username` and
// profile members) and like a post (`pk`, `code`, `user`). Every access is defensive: a missing member is null
// (and one partial_parse warning), never an error. Only a page without any recognizable profile data is an error.
package threads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const (
	platform  = ss.Threads
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"
)

var userHints = []string{"follower_count", "biography", "full_name", "is_verified", "pk", "id"}

// Module is the Threads platform module.
type Module struct{ HTTP *http.Client }

// New returns the module over a copy of hc that never follows redirects.
func New(hc *http.Client) *Module {
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Module{HTTP: &c}
}

type obj = map[string]any

// base is the origin every request goes to: Threads lives on threads.com, whatever host the URL named
// (threads.net redirects there, and redirects are not followed).
func base(ss.Target) string { return "https://www.threads.com" }

func retryAfter(h http.Header) int {
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil && s >= 0 {
		return s
	}
	return -1
}

// page fetches one page. what is "account" or "post" (the meaning of a 404).
func (m *Module) page(ctx context.Context, url, what string) ([]byte, *ss.Error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, ss.UpstreamError(platform, "could not build the Threads request")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := m.HTTP.Do(req)
	if err != nil {
		if ss.IsTimeout(err) {
			return nil, ss.UpstreamTimeout(platform)
		}
		return nil, ss.UpstreamError(platform, "could not reach Threads")
	}
	defer resp.Body.Close()
	switch code := resp.StatusCode; {
	case code == http.StatusTooManyRequests:
		return nil, ss.RateLimited(platform, retryAfter(resp.Header))
	case code == http.StatusNotFound || code == http.StatusGone:
		if what == "post" {
			return nil, ss.PostNotFound(platform, "the Threads post does not exist or was deleted")
		}
		return nil, ss.AccountNotFound(platform, "the Threads account does not exist or was deleted")
	case code != http.StatusOK:
		return nil, ss.UpstreamError(platform, fmt.Sprintf("Threads answered %d", code))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ss.IsTimeout(err) {
			return nil, ss.UpstreamTimeout(platform)
		}
		return nil, ss.UpstreamError(platform, "could not read the Threads page")
	}
	return body, nil
}

// blocks decodes the JSON of every <script type="application/json"> element; undecodable ones are skipped.
func blocks(page []byte) (out []any) {
	z := html.NewTokenizer(bytes.NewReader(page))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.StartTagToken:
			tok := z.Token()
			if tok.Data != "script" || !isJSON(tok.Attr) || z.Next() != html.TextToken {
				continue
			}
			dec := json.NewDecoder(strings.NewReader(z.Token().Data))
			dec.UseNumber()
			var v any
			if dec.Decode(&v) == nil {
				out = append(out, v)
			}
		}
	}
}

func isJSON(attrs []html.Attribute) bool {
	for _, a := range attrs {
		if a.Key == "type" && strings.EqualFold(strings.TrimSpace(a.Val), "application/json") {
			return true
		}
	}
	return false
}

// walk calls fn for every object nested in v, iteratively (hostile depth cannot overflow the stack), objects in
// sorted key order so that results do not depend on map iteration order.
func walk(v any, fn func(obj)) {
	stack := []any{v}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch x := cur.(type) {
		case obj:
			fn(x)
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Sort(sort.Reverse(sort.StringSlice(keys)))
			for _, k := range keys {
				stack = append(stack, x[k])
			}
		case []any:
			for i := len(x) - 1; i >= 0; i-- {
				stack = append(stack, x[i])
			}
		}
	}
}

func asInt(v any) *int {
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return ss.Ptr(int(i))
		}
	}
	return nil
}

func asStr(v any) string {
	s, _ := v.(string)
	return s
}

func sub(o obj, k string) obj {
	m, _ := o[k].(obj)
	return m
}

func author(d obj) string { return asStr(sub(d, "user")["username"]) }

type postObj struct {
	pk string
	d  obj
	p  ss.Post
}

// posts returns the post-like objects of the blocks (first occurrence of a pk wins) and how many were unusable
// for lack of a pk or code.
func posts(bl []any, base string) (out []postObj, skipped int) {
	seen := map[string]bool{}
	for _, b := range bl {
		walk(b, func(d obj) {
			if sub(d, "user") == nil {
				return
			}
			if _, c := d["caption"]; !c {
				if _, t := d["taken_at"]; !t {
					return
				}
			}
			var pk string
			switch v := d["pk"].(type) {
			case string:
				pk = v
			case json.Number:
				pk = v.String()
			}
			code := asStr(d["code"])
			if pk == "" || code == "" {
				skipped++
				return
			}
			if !seen[pk] {
				seen[pk] = true
				out = append(out, postObj{pk, d, toPost(d, pk, code, base)})
			}
		})
	}
	return out, skipped
}

func toPost(d obj, pk, code, base string) ss.Post {
	info := sub(d, "text_post_app_info")
	text := ""
	switch c := d["caption"].(type) {
	case obj:
		text = asStr(c["text"])
	case string:
		text = c
	}
	p := ss.Post{ID: pk, Text: strings.TrimSpace(text), Likes: asInt(d["like_count"]),
		Replies: asInt(info["direct_reply_count"]), Reposts: asInt(info["repost_count"])}
	if n, ok := d["taken_at"].(json.Number); ok {
		if sec, err := n.Float64(); err == nil {
			p.CreatedAt = ss.Ptr(time.Unix(int64(sec), 0).UTC().Format("2006-01-02T15:04:05Z"))
		}
	}
	if u := author(d); u != "" {
		p.URL = ss.Ptr(base + "/@" + u + "/post/" + code)
	}
	return p
}

// userObj is the richest object naming username, completed from the other (partial) ones; nil when none.
func userObj(bl []any, username string) obj {
	var cands []obj
	for _, b := range bl {
		walk(b, func(d obj) {
			if u := asStr(d["username"]); !strings.EqualFold(u, username) || u == "" {
				return
			}
			if _, isPost := d["code"]; isPost {
				return
			}
			for _, h := range userHints {
				if _, ok := d[h]; ok {
					cands = append(cands, d)
					return
				}
			}
		})
	}
	if len(cands) == 0 {
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool { return len(cands[i]) > len(cands[j]) })
	merged := obj{}
	for _, d := range cands {
		for k, v := range d {
			if _, ok := merged[k]; !ok && v != nil {
				merged[k] = v
			}
		}
	}
	return merged
}

func profileResult(u obj, username, kind, base string) (*ss.Result, *ss.Error) {
	if p, _ := u["is_private"].(bool); p {
		return nil, ss.AccountPrivate(platform, "the Threads account is private")
	}
	name := asStr(u["username"])
	if name == "" {
		name = username
	}
	res := &ss.Result{Platform: platform, URLKind: kind, Username: name, DisplayName: ss.NonEmpty(asStr(u["full_name"])),
		ProfileURL: ss.Ptr(base + "/@" + name), Followers: asInt(u["follower_count"])} // following, post_count: not exposed by Threads
	if b, ok := u["biography"].(string); ok {
		res.Bio = ss.Ptr(strings.TrimSpace(b))
	}
	if v, ok := u["is_verified"].(bool); ok {
		res.Verified = &v
	}
	return res, nil
}

// history is username's own posts, newest first, at most limit.
func history(ps []postObj, username string, limit int) []ss.Post {
	var mine []ss.Post
	for _, p := range ps {
		if strings.EqualFold(author(p.d), username) {
			mine = append(mine, p.p)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool { return deref(mine[i].CreatedAt) > deref(mine[j].CreatedAt) })
	if len(mine) > limit {
		mine = mine[:limit]
	}
	return mine
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// loginWall reports a page that asks for a login: a login form (password field or form posting to /login), a link
// to /login, or login-required flags in the embedded JSON. Only consulted for a page without the wanted data.
func loginWall(page []byte, bl []any) bool {
	z := html.NewTokenizer(bytes.NewReader(page))
	for tt := z.Next(); tt != html.ErrorToken; tt = z.Next() {
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		for _, a := range tok.Attr {
			switch {
			case tok.Data == "a" && a.Key == "href", tok.Data == "form" && a.Key == "action":
				if p, err := url.Parse(a.Val); err == nil && (p.Path == "/login" || strings.HasPrefix(p.Path, "/login/")) {
					return true
				}
			case tok.Data == "input" && a.Key == "type" && strings.EqualFold(a.Val, "password"):
				return true
			}
		}
	}
	found := false
	for _, b := range bl {
		walk(b, func(d obj) {
			if rl, _ := d["require_login"].(bool); rl || d["message"] == "login_required" || d["error_type"] == "login_required" {
				found = true
			}
		})
	}
	return found
}

// unrecognizable is the error for a 200 page without the wanted data: a login wall or an unparseable page.
func unrecognizable(what string, page []byte, bl []any) *ss.Error {
	if loginWall(page, bl) {
		return ss.LoginRequired(platform, "Threads requires a login to show this "+what)
	}
	return ss.UpstreamError(platform, "the Threads page contained no recognizable "+what+" data")
}

// profile reads a profile page; bad collects the unusable post objects.
func (m *Module) profile(ctx context.Context, base, username string, limit int, bad *int) (*ss.Result, *ss.Error) {
	page, err := m.page(ctx, base+"/@"+username, "account")
	if err != nil {
		return nil, err
	}
	bl := blocks(page)
	u := userObj(bl, username)
	if u == nil {
		return nil, unrecognizable("profile", page, bl)
	}
	res, err := profileResult(u, username, ss.KindProfile, base)
	if err != nil {
		return nil, err
	}
	ps, skipped := posts(bl, base)
	*bad += skipped
	res.Posts = history(ps, res.Username, limit)
	return res, nil
}

// Scrape implements socialscraper.Module.
func (m *Module) Scrape(ctx context.Context, t ss.Target, req ss.Request) (*ss.Result, error) {
	b, username, bad := base(t), t.IDs["username"], 0
	var (
		res      *ss.Result
		warnings []ss.Warning
		err      *ss.Error
	)
	if t.URLKind == ss.KindProfile {
		if res, err = m.profile(ctx, b, username, req.MaxPosts, &bad); err != nil {
			return nil, err
		}
	} else {
		if res, err = m.post(ctx, b, username, t.IDs["code"], req, &bad); err != nil {
			return nil, err
		}
		// the author's history and counters live on the profile page, a second request
		prof, perr := m.profile(ctx, b, res.Username, req.MaxPosts, &bad)
		if perr != nil {
			warnings = append(warnings, ss.Warning{Code: "history_unavailable", Message: perr.Message})
		} else {
			res.Posts = prof.Posts
			if res.DisplayName == nil {
				res.DisplayName = prof.DisplayName
			}
			if res.Bio == nil {
				res.Bio = prof.Bio
			}
			if res.Followers == nil {
				res.Followers = prof.Followers
			}
			if res.Verified == nil {
				res.Verified = prof.Verified
			}
		}
	}
	if bad > 0 {
		warnings = append(warnings, ss.Warning{Code: "partial_parse", Message: fmt.Sprintf("%d post(s) could not be parsed", bad)})
	}
	if res.Followers == nil {
		warnings = append(warnings, ss.Warning{Code: "partial_parse", Message: "Threads page did not contain the follower count"})
	}
	res.Warnings = warnings
	return res, nil
}

// post reads a post page: the post, its author's members and the replies embedded in the page.
func (m *Module) post(ctx context.Context, base, username, code string, req ss.Request, bad *int) (*ss.Result, *ss.Error) {
	page, err := m.page(ctx, base+"/@"+username+"/post/"+code, "post")
	if err != nil {
		return nil, err
	}
	bl := blocks(page)
	ps, skipped := posts(bl, base)
	*bad += skipped
	var target *postObj
	for i := range ps {
		if asStr(ps[i].d["code"]) == code {
			target = &ps[i]
			break
		}
	}
	if target == nil {
		return nil, unrecognizable("post", page, bl)
	}
	name := author(target.d)
	if name == "" {
		name = username
	}
	u := userObj(bl, name)
	if u == nil {
		u = obj{"username": name}
	}
	res, err := profileResult(u, name, ss.KindPost, base)
	if err != nil {
		return nil, err
	}
	res.Post = &target.p
	if req.MaxComments > 0 && target.p.CreatedAt != nil {
		var replies []postObj
		for _, p := range ps {
			if p.pk != target.pk && p.p.CreatedAt != nil && *p.p.CreatedAt >= *target.p.CreatedAt {
				replies = append(replies, p)
			}
		}
		sort.SliceStable(replies, func(i, j int) bool { return *replies[i].p.CreatedAt < *replies[j].p.CreatedAt })
		for _, r := range replies[:min(len(replies), req.MaxComments)] {
			// the page does not say what a reply answers: in_reply_to_id stays null
			res.Comments = append(res.Comments, ss.Comment{Post: r.p,
				Author: ss.Author{Username: author(r.d), DisplayName: ss.NonEmpty(asStr(sub(r.d, "user")["full_name"]))}})
		}
	}
	return res, nil
}
