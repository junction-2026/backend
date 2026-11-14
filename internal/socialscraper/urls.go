package socialscraper

import (
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// maxURLLength is the longest URL a scrape accepts.
const maxURLLength = 2000

const (
	Bluesky  = "bluesky"
	Mastodon = "mastodon"
	Web      = "web"
)

const (
	KindProfile = "profile"
	KindPost    = "post"
	KindPage    = "page"
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

// unsupported is the one error of a URL that cannot be fetched safely; it never repeats the URL (userinfo).
func unsupported(why string) *Error {
	return UnsupportedURL("", "the URL cannot be scraped: "+why)
}

// localHost reports a name or IP literal that must never be fetched: localhost, private, loopback,
// link-local and unspecified addresses (IPv4-mapped IPv6 included). A host whose last label starts with a digit
// but is not a valid IP is a legacy IPv4 spelling (decimal, octal, hex) and is refused too.
func localHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if a, err := netip.ParseAddr(host); err == nil {
		a = a.Unmap()
		return a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() ||
			(a.Is4() && a.As4()[0] == 0)
	}
	last := host[strings.LastIndexByte(host, '.')+1:]
	return last != "" && last[0] >= '0' && last[0] <= '9'
}

// Recognize maps an http(s) URL to a Target without a network request: unsafe URLs fail with
// unsupported_url, then Bluesky and Mastodon shapes are recognized and every other URL is a web page.
func Recognize(raw string) (Target, *Error) {
	if len(raw) > maxURLLength {
		return Target{}, unsupported("longer than 2,000 characters")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Target{}, unsupported("not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Target{}, unsupported("not an absolute http or https URL")
	}
	if u.Host == "" || u.Hostname() == "" {
		return Target{}, unsupported("no host")
	}
	if u.User != nil {
		return Target{}, unsupported("contains credentials")
	}
	if localHost(u.Hostname()) {
		return Target{}, unsupported("a local or private host")
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
	return ok(Web, KindPage, nil)
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
