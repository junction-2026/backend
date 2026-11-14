package x

import (
	"cmp"
	"encoding/json"
	"html"
	"sort"
	"strings"
	"time"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

// The structs below accept the response shapes of the X web client at least these members; unknown members are ignored.

type response struct {
	Data json.RawMessage `json:"data"`
}

type userResult struct {
	Typename       string `json:"__typename"`
	RestID         string `json:"rest_id"`
	IsBlueVerified *bool  `json:"is_blue_verified"`
	Core           struct {
		ScreenName string `json:"screen_name"`
		Name       string `json:"name"`
	} `json:"core"`
	Privacy struct {
		Protected *bool `json:"protected"`
	} `json:"privacy"`
	Legacy struct {
		ScreenName     string `json:"screen_name"`
		Name           string `json:"name"`
		Description    string `json:"description"`
		FollowersCount *int   `json:"followers_count"`
		FriendsCount   *int   `json:"friends_count"`
		StatusesCount  *int   `json:"statuses_count"`
		Protected      *bool  `json:"protected"`
		Verified       *bool  `json:"verified"`
	} `json:"legacy"`
}

func (u *userResult) screenName() string {
	return firstNonEmpty(u.Core.ScreenName, u.Legacy.ScreenName)
}
func (u *userResult) name() string { return firstNonEmpty(u.Core.Name, u.Legacy.Name) }
func (u *userResult) protected() bool {
	return (u.Privacy.Protected != nil && *u.Privacy.Protected) || (u.Legacy.Protected != nil && *u.Legacy.Protected)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

type tweetResult struct {
	Typename string       `json:"__typename"`
	RestID   string       `json:"rest_id"`
	Tweet    *tweetResult `json:"tweet"` // TweetWithVisibilityResults wraps the tweet
	Core     struct {
		UserResults struct {
			Result *userResult `json:"result"`
		} `json:"user_results"`
	} `json:"core"`
	Legacy struct {
		CreatedAt             string          `json:"created_at"`
		FullText              string          `json:"full_text"`
		InReplyToStatusIDStr  string          `json:"in_reply_to_status_id_str"`
		FavoriteCount         *int            `json:"favorite_count"`
		ReplyCount            *int            `json:"reply_count"`
		RetweetCount          *int            `json:"retweet_count"`
		RetweetedStatusResult json.RawMessage `json:"retweeted_status_result"`
	} `json:"legacy"`
	NoteTweet struct {
		Results struct {
			Result struct {
				Text string `json:"text"`
			} `json:"result"`
		} `json:"note_tweet_results"`
	} `json:"note_tweet"`
}

// real returns the tweet itself, or nil for a tombstone or anything without an id.
func (t *tweetResult) real() *tweetResult {
	if t != nil && t.Tweet != nil {
		t = t.Tweet
	}
	if t == nil || t.RestID == "" || t.Typename == "TweetTombstone" || t.Typename == "TweetUnavailable" {
		return nil
	}
	return t
}

func (t *tweetResult) author() *userResult { return t.Core.UserResults.Result }
func (t *tweetResult) isRetweet() bool {
	r := strings.TrimSpace(string(t.Legacy.RetweetedStatusResult))
	return r != "" && r != "null"
}

func (t *tweetResult) created() time.Time {
	c, _ := time.Parse(time.RubyDate, t.Legacy.CreatedAt)
	return c
}

type itemContent struct {
	ItemType     string `json:"itemType"`
	CursorType   string `json:"cursorType"`
	Value        string `json:"value"`
	TweetResults struct {
		Result *tweetResult `json:"result"`
	} `json:"tweet_results"`
}

type entry struct {
	EntryID string `json:"entryId"`
	Content struct {
		CursorType  string       `json:"cursorType"`
		Value       string       `json:"value"`
		ItemContent *itemContent `json:"itemContent"`
		Items       []struct {
			Item struct {
				ItemContent *itemContent `json:"itemContent"`
			} `json:"item"`
		} `json:"items"`
	} `json:"content"`
}

type timeline struct {
	Instructions []struct {
		Type    string  `json:"type"`
		Entries []entry `json:"entries"`
	} `json:"instructions"`
}

// tweets returns every tweet of the timeline in served order (modules flattened) and the next-page cursor.
// Pinned-tweet instructions (TimelinePinEntry) are not read: the tweet shows at its own position.
func (tl timeline) tweets() (out []*tweetResult, cursor string) {
	for _, in := range tl.Instructions {
		for _, e := range in.Entries {
			c := e.Content
			if c.CursorType == "Bottom" || c.CursorType == "ShowMore" || c.CursorType == "ShowMoreThreads" {
				cursor = c.Value
			}
			var items []*itemContent
			if c.ItemContent != nil {
				items = append(items, c.ItemContent)
			}
			for _, it := range c.Items {
				items = append(items, it.Item.ItemContent)
			}
			for _, ic := range items {
				if ic == nil {
					continue
				}
				if ic.CursorType == "Bottom" || ic.CursorType == "ShowMore" || ic.CursorType == "ShowMoreThreads" {
					cursor = ic.Value
				}
				if r := ic.TweetResults.Result; r != nil {
					out = append(out, r)
				}
			}
		}
	}
	return out, cursor
}

func decodeText(s string) string { return strings.TrimSpace(html.UnescapeString(s)) }

func postOf(t *tweetResult, username string) ss.Post {
	text := decodeText(t.Legacy.FullText)
	if n := t.NoteTweet.Results.Result.Text; n != "" { // long-form tweet: full_text is cut short
		text = strings.TrimSpace(n)
	}
	p := ss.Post{ID: t.RestID, Text: text, Likes: t.Legacy.FavoriteCount, Replies: t.Legacy.ReplyCount, Reposts: t.Legacy.RetweetCount}
	if c := t.created(); !c.IsZero() {
		p.CreatedAt = ss.Ptr(c.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if username != "" {
		p.URL = ss.Ptr("https://x.com/" + username + "/status/" + t.RestID)
	}
	return p
}

func commentOf(t *tweetResult) ss.Comment {
	a := t.author()
	var user, name string
	if a != nil {
		user, name = a.screenName(), a.name()
	}
	c := ss.Comment{Post: postOf(t, user), Author: ss.Author{Username: user, DisplayName: ss.NonEmpty(name)}}
	c.InReplyToID = ss.NonEmpty(t.Legacy.InReplyToStatusIDStr)
	return c
}

// sortTweets orders by creation time, ties by snowflake id (ids grow with time).
func sortTweets(ts []*tweetResult, desc bool) {
	sort.SliceStable(ts, func(i, j int) bool {
		a, b := ts[i], ts[j]
		c := cmp.Or(a.created().Compare(b.created()), cmp.Compare(len(a.RestID), len(b.RestID)), strings.Compare(a.RestID, b.RestID))
		if desc {
			return c > 0
		}
		return c < 0
	})
}
