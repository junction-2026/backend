// Package mastodon scrapes any Mastodon instance through its unauthenticated REST API.
package mastodon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const (
	platform = ss.Mastodon
	pageSize = 40 // Mastodon caps statuses pages at 40
	maxPages = 10
)

// Module is the Mastodon platform module.
type Module struct{ HTTP *http.Client }

// New returns the module over a copy of hc that never follows redirects: the host named in the URL is
// the only one contacted.
func New(hc *http.Client) *Module {
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Module{HTTP: &c}
}

type account struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	Acct           string `json:"acct"`
	DisplayName    string `json:"display_name"`
	Note           string `json:"note"`
	URL            string `json:"url"`
	FollowersCount *int   `json:"followers_count"`
	FollowingCount *int   `json:"following_count"`
	StatusesCount  *int   `json:"statuses_count"`
}

type statusBody struct {
	ID          string  `json:"id"`
	CreatedAt   string  `json:"created_at"`
	InReplyToID *string `json:"in_reply_to_id"`
	URL         string  `json:"url"`
	URI         string  `json:"uri"`
	Content     string  `json:"content"`
	Replies     *int    `json:"replies_count"`
	Reblogs     *int    `json:"reblogs_count"`
	Favourites  *int    `json:"favourites_count"`
}

func (s statusBody) post() (ss.Post, error) {
	if s.ID == "" {
		return ss.Post{}, errors.New("status without id")
	}
	u := s.URL
	if u == "" {
		u = s.URI
	}
	return ss.Post{ID: s.ID, Text: ss.PlainText(s.Content), CreatedAt: ss.Timestamp(s.CreatedAt), URL: ss.NonEmpty(u),
		Likes: s.Favourites, Replies: s.Replies, Reposts: s.Reblogs}, nil
}

func acct(a, username, host string) string {
	if a == "" {
		a = username
	}
	if strings.Contains(a, "@") {
		return a
	}
	return a + "@" + host
}

// get fetches one URL and maps failures to typed errors. what is "account" or "post".
func (m *Module) get(ctx context.Context, u string, what string) ([]byte, http.Header, *ss.Error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, ss.UpstreamError(platform, "could not build the Mastodon request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "junction-scraper/1")
	resp, err := m.HTTP.Do(req)
	if err != nil {
		if ss.IsTimeout(err) {
			return nil, nil, ss.UpstreamTimeout(platform)
		}
		return nil, nil, ss.UpstreamError(platform, "could not reach the Mastodon instance")
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(resp.Body)
	if rerr != nil && ss.IsTimeout(rerr) {
		return nil, nil, ss.UpstreamTimeout(platform)
	}
	notMastodon := ss.UnsupportedURL(platform, "the host does not serve the Mastodon REST API")
	code := resp.StatusCode
	switch {
	case code == http.StatusTooManyRequests:
		return nil, nil, ss.RateLimited(platform, retryAfter(resp.Header))
	case code >= 300 && code < 400:
		return nil, nil, notMastodon
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return nil, nil, ss.AccountPrivate(platform, "the Mastodon "+what+" is not public")
	case code == http.StatusNotFound || code == http.StatusGone:
		if !isJSON(body) { // a Mastodon 404 is JSON; HTML means this host is not Mastodon
			return nil, nil, notMastodon
		}
		if what == "post" {
			return nil, nil, ss.PostNotFound(platform, "the Mastodon post does not exist or was deleted")
		}
		return nil, nil, ss.AccountNotFound(platform, "the Mastodon account does not exist or was deleted")
	case code >= 400:
		return nil, nil, ss.UpstreamError(platform, fmt.Sprintf("the Mastodon instance answered %d", code))
	}
	if rerr != nil {
		return nil, nil, ss.UpstreamError(platform, "could not read the Mastodon answer")
	}
	if !isJSON(body) {
		return nil, nil, notMastodon
	}
	return body, resp.Header, nil
}

// isJSON reports a valid JSON object or array.
func isJSON(b []byte) bool {
	b = []byte(strings.TrimSpace(string(b)))
	return len(b) > 0 && (b[0] == '{' || b[0] == '[') && json.Valid(b)
}

func retryAfter(h http.Header) int {
	if n, err := strconv.Atoi(h.Get("Retry-After")); err == nil && n >= 0 {
		return n
	}
	if t, err := time.Parse(time.RFC3339, h.Get("X-Ratelimit-Reset")); err == nil { // the time the window resets
		return max(0, int(time.Until(t).Seconds()))
	}
	return -1
}

func badData() *ss.Error {
	return ss.UpstreamError(platform, "the Mastodon instance answered with data that could not be parsed")
}

func endpoint(host, path string, q url.Values) string {
	u := url.URL{Scheme: "https", Host: host, Path: path, RawQuery: q.Encode()}
	return u.String()
}

// Scrape implements socialscraper.Module.
func (m *Module) Scrape(ctx context.Context, t ss.Target, req ss.Request) (*ss.Result, error) {
	host := t.IDs["host"]
	var (
		acc    account
		status *statusBody
	)
	if t.URLKind == ss.KindPost {
		body, _, err := m.get(ctx, endpoint(host, "/api/v1/statuses/"+t.IDs["status_id"], nil), "post")
		if err != nil {
			return nil, err
		}
		var st struct {
			statusBody
			Account *account `json:"account"`
		}
		if json.Unmarshal(body, &st) != nil || st.Account == nil || st.Account.ID == "" || st.ID == "" {
			return nil, badData()
		}
		acc, status = *st.Account, &st.statusBody
	} else {
		body, _, err := m.get(ctx, endpoint(host, "/api/v1/accounts/lookup", url.Values{"acct": {t.IDs["user"]}}), "account")
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(body, &acc) != nil || acc.ID == "" {
			return nil, badData()
		}
	}
	res := &ss.Result{
		Platform: platform, URLKind: t.URLKind, Username: acct(acc.Acct, acc.Username, host),
		DisplayName: ss.NonEmpty(acc.DisplayName), Bio: ss.Ptr(ss.PlainText(acc.Note)), ProfileURL: ss.NonEmpty(acc.URL),
		Followers: acc.FollowersCount, Following: acc.FollowingCount, PostCount: acc.StatusesCount,
		// verified stays null: Mastodon has no account verification flag
	}
	res.Posts, res.Warnings = m.history(ctx, host, acc.ID, req.MaxPosts)
	if status != nil {
		post, _ := status.post()
		res.Post = &post
		if req.MaxComments > 0 {
			comments, warnings, err := m.comments(ctx, host, status.ID, req.MaxComments)
			if err != nil {
				warnings = append(warnings, ss.Warning{Code: "comments_unavailable", Message: err.Message})
			}
			res.Comments, res.Warnings = comments, append(res.Warnings, warnings...)
		}
	}
	return res, nil
}

// parseAll decodes each item; one that does not parse is skipped and reported once as partial_parse.
func parseAll[T any](items []json.RawMessage, fn func(json.RawMessage) (T, error)) (out []T, warnings []ss.Warning) {
	bad := 0
	for _, raw := range items {
		v, err := fn(raw)
		if err != nil {
			bad++
			continue
		}
		out = append(out, v)
	}
	if bad > 0 {
		warnings = append(warnings, ss.Warning{Code: "partial_parse", Message: fmt.Sprintf("%d item(s) from the instance could not be parsed", bad)})
	}
	return out, warnings
}

func parsePost(raw json.RawMessage) (ss.Post, error) {
	var s statusBody
	if err := json.Unmarshal(raw, &s); err != nil {
		return ss.Post{}, err
	}
	return s.post()
}

func (m *Module) history(ctx context.Context, host, accountID string, limit int) (posts []ss.Post, warnings []ss.Warning) {
	next := endpoint(host, "/api/v1/accounts/"+url.PathEscape(accountID)+"/statuses",
		url.Values{"limit": {strconv.Itoa(min(pageSize, limit))}, "exclude_reblogs": {"true"}})
	for range maxPages {
		body, hdr, err := m.get(ctx, next, "account")
		var items []json.RawMessage
		if err == nil && json.Unmarshal(body, &items) != nil {
			err = ss.UpstreamError(platform, "unexpected statuses response")
		}
		if err != nil {
			code := "history_unavailable"
			if len(posts) > 0 {
				code = "history_truncated"
			}
			warnings = append(warnings, ss.Warning{Code: code, Message: err.Message})
			break
		}
		page, w := parseAll(items, parsePost)
		posts, warnings = append(posts, page...), append(warnings, w...)
		link := nextLink(hdr)
		if len(posts) >= limit || len(items) == 0 || link == "" || !sameOrigin(link, host) {
			break
		}
		next = link
	}
	if len(posts) > limit {
		posts = posts[:limit]
	}
	return posts, dedupe(warnings)
}

// dedupe merges the partial_parse warnings of several pages into one.
func dedupe(w []ss.Warning) []ss.Warning {
	var out []ss.Warning
	seen := false
	for _, x := range w {
		if x.Code == "partial_parse" {
			if seen {
				continue
			}
			seen = true
		}
		out = append(out, x)
	}
	return out
}

func (m *Module) comments(ctx context.Context, host, id string, limit int) ([]ss.Comment, []ss.Warning, *ss.Error) {
	body, _, err := m.get(ctx, endpoint(host, "/api/v1/statuses/"+url.PathEscape(id)+"/context", nil), "post")
	if err != nil {
		return nil, nil, err
	}
	var ctxResp struct {
		Descendants []json.RawMessage `json:"descendants"`
	}
	if json.Unmarshal(body, &ctxResp) != nil {
		return nil, nil, ss.UpstreamError(platform, "unexpected context response")
	}
	comments, warnings := parseAll(ctxResp.Descendants, func(raw json.RawMessage) (ss.Comment, error) {
		var s struct {
			statusBody
			Account *struct {
				Username    string `json:"username"`
				Acct        string `json:"acct"`
				DisplayName string `json:"display_name"`
			} `json:"account"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return ss.Comment{}, err
		}
		p, err := s.post()
		if err != nil {
			return ss.Comment{}, err
		}
		c := ss.Comment{Post: p, InReplyToID: s.InReplyToID}
		if s.Account != nil {
			c.Author = ss.Author{Username: acct(s.Account.Acct, s.Account.Username, host), DisplayName: ss.NonEmpty(s.Account.DisplayName)}
		} else {
			c.Author = ss.Author{Username: acct("", "", host)}
		}
		return c, nil
	})
	sort.SliceStable(comments, func(i, j int) bool {
		a, b := "", ""
		if comments[i].CreatedAt != nil {
			a = *comments[i].CreatedAt
		}
		if comments[j].CreatedAt != nil {
			b = *comments[j].CreatedAt
		}
		return a < b
	})
	if len(comments) > limit {
		comments = comments[:limit]
	}
	return comments, warnings, nil
}

var linkRe = regexp.MustCompile(`<([^>]*)>([^<]*)`)

// nextLink returns the rel="next" target of the Link header, or "".
func nextLink(h http.Header) string {
	for _, line := range h.Values("Link") {
		for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
			for _, param := range strings.Split(strings.TrimRight(m[2], ", \t"), ";") {
				k, v, _ := strings.Cut(strings.TrimSpace(param), "=")
				if strings.EqualFold(k, "rel") && slices.Contains(strings.Fields(strings.Trim(v, `"`)), "next") {
					return m[1]
				}
			}
		}
	}
	return ""
}

// sameOrigin allows only https on the instance's host and the default port.
func sameOrigin(link, host string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), host) {
		return false
	}
	return u.Port() == "" || u.Port() == "443"
}
