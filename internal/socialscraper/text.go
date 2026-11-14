package socialscraper

import (
	"strings"
	"time"

	"golang.org/x/net/html"
)

// PlainText turns HTML into plain text: tags removed, entities decoded, <br> and paragraph
// boundaries become "\n", link text kept, result trimmed.
func PlainText(s string) string {
	var b strings.Builder
	newline := func() {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
	}
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.TrimSpace(b.String())
		case html.TextToken:
			b.WriteString(z.Token().Data) // Token() decodes entities
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "br":
				b.WriteByte('\n')
			case "p":
				newline()
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "p" {
				newline()
			}
		}
	}
}

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04:05.999999999"}

// Timestamp converts an ISO-8601 time to RFC 3339 in UTC with a Z suffix; nil when empty or unparseable.
// A time without an offset is taken as UTC.
func Timestamp(s string) *string {
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return Ptr(t.UTC().Format("2006-01-02T15:04:05Z"))
		}
	}
	return nil
}
