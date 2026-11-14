package socialscraper

import (
	"net/url"
	"regexp"
	"strings"
)

const (
	Bluesky  = "bluesky"
	Mastodon = "mastodon"
)

const (
	KindProfile = "profile"
	KindPost    = "post"
)

var (
	// Mastodon accepts any host that is not a listed platform. A real TLD starts with a letter, which drops every
	// IPv4 spelling (decimal, octal, hex) and IPv6 (":" is not a label character) as well as dotless names.
	hostnameRe  = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)*\.[a-z][a-z0-9-]*$`)
	listedHosts = set("bsky.app")
)

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// normalize lower-cases the host, drops one www./m./mobile. prefix and splits the path (query string and
// fragment are ignored). Segments stay percent-encoded, so an encoded "/" cannot add a segment.
func normalize(u *url.URL) (host string, seg []string) {
	host = strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	for _, p := range []string{"www.", "m.", "mobile."} {
		if strings.HasPrefix(host, p) {
			host = host[len(p):]
			break
		}
	}
	for _, s := range strings.Split(u.EscapedPath(), "/") {
		if s != "" {
			seg = append(seg, s)
		}
	}
	return host, seg
}

// Recognize maps an absolute http(s) URL to a Target, or fails with unsupported_url. It needs no
// registered module and makes no network request.
func Recognize(raw string) (Target, *Error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Target{}, UnsupportedURL("", "not a recognized public post or profile URL of a supported platform")
	}
	host, s := normalize(u)
	t := Target{URL: u}
	ok := func(platform, kind string, ids map[string]string) (Target, *Error) {
		t.Platform, t.URLKind, t.IDs = platform, kind, ids
		return t, nil
	}
	switch host {
	case "bsky.app":
		if len(s) == 2 && s[0] == "profile" {
			return ok(Bluesky, KindProfile, map[string]string{"actor": s[1]})
		}
		if len(s) == 4 && s[0] == "profile" && s[2] == "post" {
			return ok(Bluesky, KindPost, map[string]string{"actor": s[1], "rkey": s[3]})
		}
	default:
		if t, ok2 := recognizeMastodon(t, host, s); ok2 {
			return t, nil
		}
	}
	return Target{}, UnsupportedURL("", "not a recognized public post or profile URL of a supported platform")
}

func recognizeMastodon(t Target, host string, s []string) (Target, bool) {
	if listedHosts[host] || !hostnameRe.MatchString(host) || len(s) == 0 {
		return t, false
	}
	t.Platform = Mastodon
	set := func(kind string, user, status string) (Target, bool) {
		t.URLKind = kind
		t.IDs = map[string]string{"host": host, "user": user}
		if status != "" {
			t.IDs["status_id"] = status
		}
		return t, true
	}
	if strings.HasPrefix(s[0], "@") && len(s[0]) > 1 {
		user, rest := s[0][1:], s[1:]
		if len(rest) == 0 {
			return set(KindProfile, user, "")
		}
		if len(rest) == 1 && isDigits(rest[0]) {
			return set(KindPost, user, rest[0])
		}
	}
	if s[0] == "users" && len(s) >= 2 {
		if len(s) == 2 {
			return set(KindProfile, s[1], "")
		}
		if len(s) == 4 && s[2] == "statuses" && isDigits(s[3]) {
			return set(KindPost, s[1], s[3])
		}
	}
	return t, false
}
