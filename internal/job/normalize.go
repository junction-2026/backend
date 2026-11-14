package job

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/text/unicode/norm"
)

// Normalize computes normalized_content: for text/html the text content with
// tags removed, then Unicode NFC, CRLF/CR -> LF and trimmed.
func Normalize(content, contentType string) string {
	if strings.EqualFold(mediaType(contentType), "text/html") {
		content = htmlText(content)
	}
	content = norm.NFC.String(content)
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.TrimSpace(content)
}

func mediaType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(ct)
}

var blockTags = map[string]bool{"p": true, "div": true, "br": true, "li": true, "tr": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "blockquote": true, "pre": true, "section": true, "article": true}

// htmlText concatenates text tokens, skipping script/style bodies and breaking
// lines at block-level tags so words of adjacent paragraphs do not glue.
func htmlText(s string) string {
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(s))
	skip := ""
	for {
		switch z.Next() {
		case html.ErrorToken:
			return b.String()
		case html.TextToken:
			if skip == "" {
				b.Write(z.Text())
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			tt := z.Token()
			switch {
			case skip != "":
				if tt.Type == html.EndTagToken && tt.Data == skip {
					skip = ""
				}
			case tt.Type == html.StartTagToken && (tt.Data == "script" || tt.Data == "style"):
				skip = tt.Data
			case blockTags[tt.Data] && b.Len() > 0 && !strings.HasSuffix(b.String(), "\n"):
				b.WriteByte('\n')
			}
		}
	}
}
