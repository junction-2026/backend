package adapter

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/junction-2026/backend/internal/registry"
)

// foundResult is one provider result with the rank it had and the entry that found it.
type foundResult struct {
	rank int
	serperResult
	q     *searchQuery
	ts    time.Time
	hasTS bool
}

// article is the shared Article object.
type article struct {
	Title    string  `json:"title"`
	URL      string  `json:"url"`
	Domain   string  `json:"domain"`
	Source   *string `json:"source"`
	Date     *string `json:"date"`
	Snippet  *string `json:"snippet"`
	Language string  `json:"language"`
	Query    string  `json:"query"`
	Origin   string  `json:"origin"`
}

// rankResults tags rs with ranks; now is one instant per run so equal relative dates compare equal.
func rankResults(rs []serperResult, q *searchQuery, now time.Time) []foundResult {
	out := make([]foundResult, len(rs))
	for i, r := range rs {
		out[i] = foundResult{rank: i + 1, serperResult: r, q: q}
		out[i].ts, out[i].hasTS = parseNewsDate(r.Date, now)
	}
	return out
}

var relDateRe = regexp.MustCompile(`(?i)^(\d+)\s+(minute|hour|day|week|month|year)s?\s+ago$`)

var dateLayouts = []string{time.RFC3339, "2006-01-02", "Jan 2, 2006", "January 2, 2006", "2 Jan 2006", "2 January 2006", "Jan 2, 2006, 3:04 PM"}

// parseNewsDate reads the provider's date text where it can (relative or common absolute forms).
func parseNewsDate(s string, now time.Time) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if m := relDateRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		switch strings.ToLower(m[2]) {
		case "minute":
			return now.Add(-time.Duration(n) * time.Minute), true
		case "hour":
			return now.Add(-time.Duration(n) * time.Hour), true
		case "day":
			return now.AddDate(0, 0, -n), true
		case "week":
			return now.AddDate(0, 0, -7*n), true
		case "month":
			return now.AddDate(0, -n, 0), true
		}
		return now.AddDate(-n, 0, 0), true
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// normalizeURL returns the URL with lower-case scheme and host and no fragment, and its domain; ok is false
// for anything but an absolute http(s) URL.
func normalizeURL(raw string) (norm, domain string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", "", false
	}
	u.Host, u.Fragment, u.RawFragment = strings.ToLower(u.Host), "", ""
	return u.String(), strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."), true
}

// mergeArticles orders the results of the succeeded entries (provider rank, then dated before undated and newest
// first, then entry order), drops duplicates and non-http(s) URLs and applies per_domain and max_results.
func mergeArticles(cfg registry.NewsSearch, queries []*searchQuery) []article {
	var all []foundResult
	for _, q := range queries {
		if q.Status == "succeeded" {
			all = append(all, q.found...)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		switch {
		case a.rank != b.rank:
			return a.rank < b.rank
		case a.hasTS != b.hasTS:
			return a.hasTS
		case a.hasTS && !a.ts.Equal(b.ts):
			return a.ts.After(b.ts)
		}
		return a.q.idx < b.q.idx
	})
	out := []article{}
	seen, perDomain := map[string]bool{}, map[string]int{}
	for _, f := range all {
		if len(out) >= cfg.MaxResults {
			break
		}
		norm, domain, ok := normalizeURL(f.Link)
		if !ok || seen[norm] || perDomain[domain] >= cfg.PerDomain {
			continue
		}
		seen[norm] = true
		perDomain[domain]++
		title := strings.TrimSpace(f.Title)
		if title == "" {
			title = norm
		}
		out = append(out, article{Title: title, URL: norm, Domain: domain, Source: nilIfBlank(f.Source),
			Date: nilIfBlank(f.Date), Snippet: nilIfBlank(f.Snippet), Language: f.q.Language, Query: f.q.Q, Origin: f.q.Origin})
	}
	return out
}

func nilIfBlank(s string) *string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return &s
}
