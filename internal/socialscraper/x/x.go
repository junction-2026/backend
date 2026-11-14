// Package x scrapes X (Twitter) through the web app's GraphQL endpoints with the auth_token and ct0 cookies of
// one logged-in session. The interface is unofficial: query ids and feature flags live in endpoints.go.
package x

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const (
	platform      = ss.Twitter
	pageSize      = 20
	maxTweetPages = 10 // shortcut: bounds one profile scrape; upgrade if accounts with long retweet runs matter
	maxReplyPages = 5
)

// Module is the X platform module. The cookies are read from the environment on every scrape.
type Module struct {
	HTTP   *http.Client
	Getenv func(string) string
	Now    func() time.Time
}

// New returns the module over a copy of hc that never follows redirects: the cookies go to x.com only.
func New(hc *http.Client, getenv func(string) string) *Module {
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Module{HTTP: &c, Getenv: getenv, Now: time.Now}
}

// session holds the cookies of one scrape; it is never logged or put into an error.
type session struct {
	m         *Module
	authToken string
	ct0       string
}

func (m *Module) Scrape(ctx context.Context, t ss.Target, req ss.Request) (*ss.Result, error) {
	s := &session{m: m, authToken: strings.TrimSpace(m.Getenv("TWITTER_AUTH_TOKEN")), ct0: strings.TrimSpace(m.Getenv("TWITTER_CT0"))}
	var missing []string
	if s.authToken == "" {
		missing = append(missing, "TWITTER_AUTH_TOKEN")
	}
	if s.ct0 == "" {
		missing = append(missing, "TWITTER_CT0")
	}
	if len(missing) > 0 {
		return nil, ss.PlatformNotConfigured(platform, missing...)
	}

	var (
		user     *userResult
		focal    *tweetResult
		comments []ss.Comment
		warnings []ss.Warning
	)
	if t.URLKind == ss.KindPost {
		var err error
		if focal, comments, warnings, err = s.tweet(ctx, t.IDs["status_id"], req.MaxComments); err != nil {
			return nil, err
		}
		if user = focal.author(); user == nil || user.RestID == "" {
			return nil, ss.UpstreamError(platform, "X returned a tweet without its author")
		}
	} else {
		var err error
		if user, err = s.user(ctx, t.IDs["username"]); err != nil {
			return nil, err
		}
	}
	if user.protected() {
		return nil, ss.AccountPrivate(platform, "the X account is protected")
	}

	posts, err := s.userTweets(ctx, user, req.MaxPosts)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		warnings = append(warnings, ss.Warning{Code: "history_unavailable", Message: "the recent posts could not be read"})
	}
	name := user.screenName()
	res := &ss.Result{
		Platform: platform, URLKind: t.URLKind, Username: name, DisplayName: ss.NonEmpty(user.name()),
		Bio: ss.NonEmpty(decodeText(user.Legacy.Description)), ProfileURL: ss.Ptr("https://x.com/" + name),
		Followers: user.Legacy.FollowersCount, Following: user.Legacy.FriendsCount, PostCount: user.Legacy.StatusesCount,
		Posts: posts, Comments: comments, Warnings: warnings,
	}
	if user.IsBlueVerified != nil || user.Legacy.Verified != nil {
		res.Verified = ss.Ptr((user.IsBlueVerified != nil && *user.IsBlueVerified) || (user.Legacy.Verified != nil && *user.Legacy.Verified))
	}
	if focal != nil {
		p := postOf(focal, name)
		res.Post = &p
	}
	return res, nil
}

// user looks a user up by screen name.
func (s *session) user(ctx context.Context, screenName string) (*userResult, error) {
	var d struct {
		User struct {
			Result *userResult `json:"result"`
		} `json:"user"`
	}
	if err := s.call(ctx, opUserByScreenName, map[string]any{"screen_name": screenName, "withSafetyModeUserFields": true}, &d); err != nil {
		return nil, err
	}
	u := d.User.Result
	if u == nil || u.Typename == "UserUnavailable" || u.RestID == "" { // missing, suspended or deactivated
		return nil, ss.AccountNotFound(platform, "the X account does not exist")
	}
	return u, nil
}

// userTweets returns the user's own tweets (no retweets), newest first, at most limit, following the bottom cursor.
func (s *session) userTweets(ctx context.Context, u *userResult, limit int) ([]ss.Post, error) {
	var own []*tweetResult
	cursor := ""
	for page := 0; page < maxTweetPages && len(own) < limit; page++ {
		vars := map[string]any{"userId": u.RestID, "count": pageSize, "includePromotedContent": false, "withVoice": true, "withV2Timeline": true}
		if cursor != "" {
			vars["cursor"] = cursor
		}
		var d struct {
			User struct {
				Result struct {
					Timeline   struct{ Timeline timeline } `json:"timeline"`
					TimelineV2 struct{ Timeline timeline } `json:"timeline_v2"`
				} `json:"result"`
			} `json:"user"`
		}
		if err := s.call(ctx, opUserTweets, vars, &d); err != nil {
			return nil, err
		}
		tl := d.User.Result.Timeline.Timeline
		if len(tl.Instructions) == 0 {
			tl = d.User.Result.TimelineV2.Timeline
		}
		all, next := tl.tweets()
		for _, r := range all {
			if tw := r.real(); tw != nil && !tw.isRetweet() && tw.author() != nil && tw.author().RestID == u.RestID {
				own = append(own, tw)
			}
		}
		if next == "" || next == cursor || len(all) == 0 {
			break
		}
		cursor = next
	}
	sortTweets(own, true)
	if len(own) > limit {
		own = own[:limit]
	}
	posts := make([]ss.Post, len(own))
	for i, t := range own {
		posts[i] = postOf(t, u.screenName())
	}
	return posts, nil
}

// tweet reads the tweet and, while fewer than maxComments direct replies are known, further reply pages.
// A failed reply page becomes a comments_unavailable warning; a failed first request is an error.
func (s *session) tweet(ctx context.Context, id string, maxComments int) (*tweetResult, []ss.Comment, []ss.Warning, error) {
	var focal *tweetResult
	var replies []*tweetResult
	var warnings []ss.Warning
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page == 0 || (maxComments > 0 && cursor != "" && len(replies) < maxComments && page < maxReplyPages); page++ {
		vars := map[string]any{"focalTweetId": id, "referrer": "tweet", "with_rux_injections": false, "includePromotedContent": false,
			"withCommunity": true, "withQuickPromoteEligibilityTweetFields": true, "withBirdwatchNotes": true, "withVoice": true}
		if cursor != "" {
			vars["cursor"] = cursor
		}
		var d struct {
			Conversation timeline `json:"threaded_conversation_with_injections_v2"`
		}
		if err := s.call(ctx, opTweetDetail, vars, &d); err != nil {
			if page == 0 {
				return nil, nil, nil, err
			}
			if ctx.Err() != nil {
				return nil, nil, nil, ctx.Err()
			}
			warnings = append(warnings, ss.Warning{Code: "comments_unavailable", Message: "the replies could not be read"})
			break
		}
		all, next := d.Conversation.tweets()
		for _, r := range all {
			tw := r.real()
			switch {
			case tw == nil:
			case tw.RestID == id:
				if focal == nil {
					focal = tw
				}
			case tw.Legacy.InReplyToStatusIDStr == id && !seen[tw.RestID]:
				seen[tw.RestID] = true
				replies = append(replies, tw)
			}
		}
		if page == 0 && focal == nil {
			return nil, nil, nil, ss.PostNotFound(platform, "the X post does not exist or was deleted")
		}
		if next == cursor {
			break
		}
		cursor = next
	}
	if len(replies) > maxComments {
		replies = replies[:maxComments]
	}
	sortTweets(replies, false)
	comments := make([]ss.Comment, len(replies))
	for i, r := range replies {
		comments[i] = commentOf(r)
	}
	return focal, comments, warnings, nil
}

// call sends one GraphQL GET and decodes its "data" member into out. Every failure is a typed error with a
// fixed message: upstream text can echo request headers, so only the Go error type is logged.
func (s *session) call(ctx context.Context, op operation, vars map[string]any, out any) error {
	q := url.Values{}
	q.Set("variables", mustJSON(vars))
	q.Set("features", mustJSON(op.Features))
	if len(op.FieldToggles) > 0 {
		q.Set("fieldToggles", mustJSON(op.FieldToggles))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, graphqlBase+op.ID+"/"+op.Name+"?"+q.Encode(), nil)
	if err != nil {
		return ss.UpstreamError(platform, "could not build the X request")
	}
	h := req.Header
	h.Set("Authorization", "Bearer "+bearer)
	h.Set("Cookie", "auth_token="+s.authToken+"; ct0="+s.ct0)
	h.Set("X-Csrf-Token", s.ct0)
	h.Set("X-Twitter-Auth-Type", "OAuth2Session")
	h.Set("X-Twitter-Active-User", "yes")
	h.Set("X-Twitter-Client-Language", "en")
	h.Set("Accept", "*/*")
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")

	resp, err := s.m.HTTP.Do(req)
	if err != nil {
		return transportError(err)
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(resp.Body)
	switch code := resp.StatusCode; {
	case code == http.StatusTooManyRequests:
		return ss.RateLimited(platform, s.retryAfter(resp.Header))
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return ss.UpstreamError(platform, "X rejected the configured session cookies")
	case code != http.StatusOK:
		return ss.UpstreamError(platform, "X answered with an error")
	}
	if rerr != nil {
		return transportError(rerr)
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return ss.UpstreamError(platform, "X returned unparseable data")
	}
	if len(r.Data) == 0 || string(r.Data) == "null" { // errors only
		return ss.UpstreamError(platform, "X answered with an error")
	}
	if err := json.Unmarshal(r.Data, out); err != nil {
		slog.Warn("x response not understood", "error_type", fmt.Sprintf("%T", err))
		return ss.UpstreamError(platform, "X returned unparseable data")
	}
	return nil
}

func transportError(err error) error {
	if ss.IsTimeout(err) {
		return ss.UpstreamTimeout(platform)
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	slog.Warn("x request failed", "error_type", fmt.Sprintf("%T", err))
	return ss.UpstreamError(platform, "could not reach X")
}

// retryAfter is the seconds until the epoch in x-rate-limit-reset (-1 when absent or unreadable).
func (s *session) retryAfter(h http.Header) int {
	reset, err := strconv.ParseInt(strings.TrimSpace(h.Get("X-Rate-Limit-Reset")), 10, 64)
	if err != nil {
		return -1
	}
	return max(0, int(reset-s.m.Now().Unix()))
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // maps of strings, numbers and bools only
	}
	return string(b)
}
