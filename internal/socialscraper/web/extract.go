package web

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/markusmobius/go-trafilatura"
	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const maxTextChars = 20000

// mainText extracts the main content of an HTML page with go-trafilatura: paragraphs joined by "\n",
// comments and boilerplate left out. An extraction failure is an empty text.
func mainText(page string, u *url.URL) string {
	if strings.TrimSpace(page) == "" {
		return ""
	}
	res, err := trafilatura.Extract(strings.NewReader(page), trafilatura.Options{
		OriginalURL: u, ExcludeComments: true, EnableFallback: true, HtmlDateMode: trafilatura.Disabled,
	})
	if err != nil || res == nil {
		return ""
	}
	if res.ContentNode == nil { // ContentText has no paragraph breaks; the node tree has
		return joinParagraphs(strings.Split(res.ContentText, "\n"))
	}
	var b strings.Builder
	blockText(&b, res.ContentNode)
	return joinParagraphs(strings.Split(b.String(), "\n"))
}

// blockElements end a line in the extracted text.
var blockElements = map[string]bool{"p": true, "div": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "li": true, "ul": true, "ol": true, "blockquote": true, "pre": true, "table": true, "tr": true, "br": true,
	"section": true, "article": true, "dd": true, "dt": true, "figcaption": true}

func blockText(b *strings.Builder, n *html.Node) {
	block := n.Type == html.ElementNode && blockElements[n.Data]
	if block {
		b.WriteByte('\n')
	}
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		blockText(b, c)
	}
	if block {
		b.WriteByte('\n')
	}
}

func joinParagraphs(lines []string) string {
	out := lines[:0:0]
	for _, l := range lines {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// capText cuts text at the last whitespace at or before character maxTextChars.
func capText(text string) (string, bool) {
	if utf8.RuneCountInString(text) <= maxTextChars {
		return text, false
	}
	r := []rune(text)
	cut := maxTextChars
	// r[maxTextChars] is the first dropped character: whitespace there means the cut is already at a boundary.
	for cut > 0 && !unicode.IsSpace(r[cut]) && !unicode.IsSpace(r[cut-1]) {
		cut--
	}
	for cut > 0 && unicode.IsSpace(r[cut-1]) {
		cut--
	}
	return string(r[:cut]), true
}

var (
	mdFence    = regexp.MustCompile("^\\s*(```|~~~)")
	mdHeading  = regexp.MustCompile(`^\s{0,3}#{1,6}\s+`)
	mdQuote    = regexp.MustCompile(`^\s*>+\s?`)
	mdList     = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s+`)
	mdRule     = regexp.MustCompile(`^\s*([-*_=]\s*){3,}$`)
	mdImage    = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdRefLink  = regexp.MustCompile(`\[([^\]]+)\]\[[^\]]*\]`)
	mdEmphasis = regexp.MustCompile(`(\*\*|__|~~|\*|` + "`" + `)`)
	mdUnder    = regexp.MustCompile(`(^|[\s(])_([^_\s][^_]*)_([\s).,;:!?]|$)`)
	mdTags     = regexp.MustCompile(`<[^>]+>`)
)

// stripMarkdown reduces Markdown to plain text: headings, emphasis, list markers, code fences, images and
// link targets removed (link labels kept), paragraphs joined by "\n".
func stripMarkdown(md string) string {
	var lines []string
	joinPrev := false // the previous line is a plain text line: a wrapped line continues its paragraph
	for _, l := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if mdFence.MatchString(l) || mdRule.MatchString(l) {
			joinPrev = false
			continue
		}
		plain := !mdHeading.MatchString(l) && !mdList.MatchString(l) && strings.TrimSpace(l) != ""
		l = mdHeading.ReplaceAllString(l, "")
		l = mdQuote.ReplaceAllString(l, "")
		l = mdList.ReplaceAllString(l, "")
		l = mdImage.ReplaceAllString(l, "")
		l = mdLink.ReplaceAllString(l, "$1")
		l = mdRefLink.ReplaceAllString(l, "$1")
		l = mdTags.ReplaceAllString(l, "")
		l = mdEmphasis.ReplaceAllString(l, "")
		l = mdUnder.ReplaceAllString(l, "$1$2$3")
		if plain && joinPrev && len(lines) > 0 {
			lines[len(lines)-1] += " " + l
		} else {
			lines = append(lines, l)
		}
		joinPrev = plain
	}
	return joinParagraphs(lines)
}

// meta is the page metadata of the shared "Web page metadata members".
type meta struct {
	title, siteName, author, description, lang, canonical *string
	publishedAt                                           *string
}

// docInfo collects the raw metadata sources of one document.
type docInfo struct {
	htmlLang, titleTag string
	metas              map[string]string // lower-cased name or property -> first content
	canonicalLink      string
	ld                 []map[string]any
	forms              []formInfo
	passwordInput      bool
}

type formInfo struct{ attrs, text string }

func parseDoc(page string) *docInfo {
	d := &docInfo{metas: map[string]string{}}
	root, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return d
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			attr := func(k string) string {
				for _, a := range n.Attr {
					if a.Key == k {
						return a.Val
					}
				}
				return ""
			}
			switch n.Data {
			case "html":
				d.htmlLang = strings.TrimSpace(attr("lang"))
			case "title":
				if d.titleTag == "" {
					d.titleTag = text(n)
				}
			case "meta":
				k := strings.ToLower(strings.TrimSpace(firstNonEmpty(attr("property"), attr("name"))))
				if c := strings.TrimSpace(attr("content")); k != "" && c != "" && d.metas[k] == "" {
					d.metas[k] = c
				}
			case "link":
				if d.canonicalLink == "" && containsWord(strings.ToLower(attr("rel")), "canonical") {
					d.canonicalLink = strings.TrimSpace(attr("href"))
				}
			case "script":
				if strings.EqualFold(strings.TrimSpace(attr("type")), "application/ld+json") {
					d.ld = append(d.ld, parseLD(text(n))...)
				}
			case "input":
				if strings.EqualFold(strings.TrimSpace(attr("type")), "password") {
					d.passwordInput = true
				}
			case "form":
				d.forms = append(d.forms, formInfo{
					attrs: strings.ToLower(attr("action") + " " + attr("id") + " " + attr("class") + " " + attr("name")),
					text:  strings.ToLower(text(n)),
				})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return d
}

func containsWord(s, w string) bool {
	for _, f := range strings.Fields(s) {
		if f == w {
			return true
		}
	}
	return false
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// text is the concatenated text content of n, whitespace collapsed.
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// parseLD returns the JSON-LD objects of one script: a top-level object, array items and @graph items.
func parseLD(raw string) []map[string]any {
	var v any
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &v) != nil {
		return nil
	}
	var out []map[string]any
	var add func(v any)
	add = func(v any) {
		switch t := v.(type) {
		case []any:
			for _, i := range t {
				add(i)
			}
		case map[string]any:
			out = append(out, t)
			if g, ok := t["@graph"]; ok {
				add(g)
			}
		}
	}
	add(v)
	return out
}

// ldString returns the first non-empty string of key over the JSON-LD objects, with name reading the "name" of
// an object value (author, publisher), also inside arrays.
func (d *docInfo) ldString(key string, nested bool) string {
	for _, o := range d.ld {
		if s := ldValue(o[key], nested); s != "" {
			return s
		}
	}
	return ""
}

func ldValue(v any, nested bool) string {
	switch t := v.(type) {
	case string:
		if !nested {
			return strings.TrimSpace(t)
		}
		return "" // a bare string is a name only for authors; callers of nested handle it below
	case map[string]any:
		if nested {
			s, _ := t["name"].(string)
			return strings.TrimSpace(s)
		}
	case []any:
		var names []string
		for _, i := range t {
			if s := ldValue(i, nested); s != "" {
				names = append(names, s)
			}
		}
		return strings.Join(names, ", ")
	}
	return ""
}

// ldAuthor reads author like ldValue(nested) but also accepts plain strings.
func (d *docInfo) ldAuthor() string {
	for _, o := range d.ld {
		switch t := o["author"].(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				return s
			}
		default:
			if s := ldValue(t, true); s != "" {
				return s
			}
			if arr, ok := t.([]any); ok { // an array of plain names
				var names []string
				for _, i := range arr {
					if s, ok := i.(string); ok && strings.TrimSpace(s) != "" {
						names = append(names, strings.TrimSpace(s))
					}
				}
				if len(names) > 0 {
					return strings.Join(names, ", ")
				}
			}
		}
	}
	return ""
}

func absHTTP(raw string) *string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	return ss.Ptr(u.String())
}

var dateLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05-0700", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02", time.RFC1123Z, time.RFC1123}

// parseTime returns an ISO UTC timestamp or "" when no layout fits (a time without an offset is UTC).
func parseTime(s string) string {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC().Format("2006-01-02T15:04:05Z")
		}
	}
	return ""
}

// metadata applies the precedence rules of the local spec.
func (d *docInfo) metadata() meta {
	m := meta{}
	set := func(dst **string, v ...string) {
		if s := firstNonEmpty(v...); s != "" {
			*dst = &s
		}
	}
	set(&m.title, d.ldString("headline", false), d.metas["og:title"], d.titleTag)
	set(&m.siteName, d.metas["og:site_name"], d.ldString("publisher", true))
	author := d.metas["article:author"]
	if strings.HasPrefix(strings.ToLower(author), "http") { // a profile URL is not a name
		author = ""
	}
	set(&m.author, d.ldAuthor(), d.metas["author"], author)
	set(&m.description, d.metas["og:description"], d.metas["description"])
	set(&m.lang, d.htmlLang, strings.ReplaceAll(d.metas["og:locale"], "_", "-"))
	for _, s := range []string{d.ldString("datePublished", false), d.metas["article:published_time"]} {
		if t := parseTime(s); t != "" {
			m.publishedAt = &t
			break
		}
	}
	if c := absHTTP(d.canonicalLink); c != nil {
		m.canonical = c
	} else {
		m.canonical = absHTTP(d.metas["og:url"])
	}
	return m
}

// loginWall reports the markers of a login page: a password input, a log-in or sign-in form, or a login path.
func (d *docInfo) loginWall(finalPath string) bool {
	if d.passwordInput || loginPath(finalPath) {
		return true
	}
	for _, f := range d.forms {
		for _, w := range []string{"login", "log-in", "log_in", "signin", "sign-in", "sign_in"} {
			if strings.Contains(f.attrs, w) {
				return true
			}
		}
		for _, w := range []string{"log in", "sign in", "login", "signin"} {
			if strings.Contains(f.text, w) {
				return true
			}
		}
	}
	return false
}

func loginPath(p string) bool {
	p = strings.ToLower(p)
	for _, base := range []string{"/login", "/signin", "/accounts/login"} {
		if p == base || strings.HasPrefix(p, base+"/") {
			return true
		}
	}
	return false
}

// registeredDomain is the public-suffix registered domain of host, nil for IP hosts and public suffixes.
func registeredDomain(host string) *string {
	d, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return nil
	}
	return &d
}
