package adapter

import (
	"html"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// snippetText removes the HTML markup of a search snippet and decodes its character references; nothing else changes.
func snippetText(s string) string { return html.UnescapeString(htmlTagRe.ReplaceAllString(s, "")) }

// cutRunes is the prefix of s of at most n runes.
func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// fold maps every rune to the smallest member of its Unicode simple-folding orbit (case-insensitive comparison).
func fold(s string) string {
	return strings.Map(func(r rune) rune {
		m := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			m = min(m, f)
		}
		return m
	}, s)
}

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

// digitRuns lists the maximal ASCII digit runs of s in order of appearance.
func digitRuns(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !isASCIIDigit(r) })
}

// matchedEntities: the entities occurring in passage (case-insensitive), in entities order, without duplicates.
func matchedEntities(entities []string, passage string) []string {
	out, seen, p := []string{}, map[string]bool{}, fold(passage)
	for _, e := range entities {
		if e = strings.TrimSpace(e); e != "" && !seen[e] && strings.Contains(p, fold(e)) {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

// matchedNumbers: the digit runs of the claim text that occur in passage as a run not adjacent to another digit.
func matchedNumbers(text, passage string) []string {
	in := map[string]bool{}
	for _, n := range digitRuns(passage) {
		in[n] = true
	}
	out, seen := []string{}, map[string]bool{}
	for _, n := range digitRuns(text) {
		if in[n] && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// searchMode is fulltext for a claim about a number, a date or an event, else title.
func searchMode(c claim) string {
	if strings.IndexFunc(c.Text, isASCIIDigit) >= 0 {
		return "fulltext"
	}
	for _, e := range c.Entities {
		if strings.IndexFunc(e, func(r rune) bool { return !unicode.IsDigit(r) && !unicode.IsSpace(r) && !unicode.IsPunct(r) }) >= 0 {
			return "title"
		}
	}
	return "fulltext"
}

// normalizeQuery is the cache-key form of a query: NFC, trimmed, whitespace collapsed, lower-cased.
func normalizeQuery(q string) string {
	return strings.ToLower(strings.Join(strings.Fields(norm.NFC.String(q)), " "))
}

// entityTitle reports whether title equals an entity or "entity (qualifier)", case-insensitively.
func entityTitle(title string, entities []string) bool {
	t := fold(strings.TrimSpace(title))
	for _, e := range entities {
		e = fold(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if t == e {
			return true
		}
		if rest, ok := strings.CutPrefix(t, e+" ("); ok && strings.HasSuffix(rest, ")") && len(rest) > 1 {
			return true
		}
	}
	return false
}
