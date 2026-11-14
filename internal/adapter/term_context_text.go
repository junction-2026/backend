package adapter

import (
	"strings"
	"unicode"
)

// Pure text helpers of term_context. All positions are rune indexes.

// foldRune is the smallest member of r's Unicode simple-folding orbit (as fold, for one rune).
func foldRune(r rune) rune {
	m := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		m = min(m, f)
	}
	return m
}

// wordRune: a letter or a digit. shortcut: combining marks do not count, as the shared contract says "letter or digit"; revisit if scripts with spacing marks (Devanagari, Arabic vowels) mis-split terms.
func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// findTerm returns the first occurrence of pat in text, compared with Unicode simple case folding, with neither a
// letter nor a digit directly before or after it; ok is false when there is none.
func findTerm(text, pat []rune) (start int, ok bool) {
	if len(pat) == 0 {
		return 0, false
	}
	fp := make([]rune, len(pat))
	for i, r := range pat {
		fp[i] = foldRune(r)
	}
	for i := 0; i+len(pat) <= len(text); i++ {
		if i > 0 && wordRune(text[i-1]) {
			continue
		}
		if end := i + len(pat); end < len(text) && wordRune(text[end]) {
			continue
		}
		match := true
		for j, r := range fp {
			if foldRune(text[i+j]) != r {
				match = false
				break
			}
		}
		if match {
			return i, true
		}
	}
	return 0, false
}

func hasLetter(s string) bool { return strings.IndexFunc(s, unicode.IsLetter) >= 0 }

// sentenceEnd reports whether r[i] ends a sentence: a terminator followed by whitespace (the end of the text also ends one).
func sentenceEnd(r []rune, i int) bool {
	switch r[i] {
	case '.', '!', '?', '…', '。', '！', '？', '؟':
		return i+1 == len(r) || unicode.IsSpace(r[i+1])
	}
	return false
}

// sentenceAround is the sentence of text around the occurrence [s, e): the longest part that crosses no sentence
// boundary (a line break, or a terminator followed by whitespace), trimmed. A longer sentence than limit is cut to a
// window of at most limit runes that contains the occurrence (the caller guarantees e-s <= limit).
func sentenceAround(text []rune, s, e, limit int) string {
	start := 0
	for i := s - 1; i >= 0; i-- {
		if text[i] == '\n' || sentenceEnd(text, i) {
			start = i + 1
			break
		}
	}
	end := len(text)
	for i := max(e-1, 0); i < len(text); i++ { // a terminator ending the term itself ends the sentence; a line break inside it does not
		if i >= e && text[i] == '\n' {
			end = i
			break
		}
		if sentenceEnd(text, i) {
			end = i + 1
			break
		}
	}
	if end-start > limit {
		ws := min(max(s-(limit-(e-s))/2, start), end-limit)
		we := ws + limit
		for ws > start && ws < s && !unicode.IsSpace(text[ws-1]) { // avoid starting or ending mid-word when there is room
			ws++
		}
		for we < end && we > e && !unicode.IsSpace(text[we]) {
			we--
		}
		start, end = ws, we
	}
	return strings.TrimSpace(string(text[start:end]))
}

// leadSentence is the first sentence of a plain-text lead: up to and including the first `.`, `!`, `?`, `。` or `؟`
// that is followed by whitespace or the end and is outside parentheses and square brackets; the whole lead when there
// is none. It is a prefix of lead (trailing whitespace removed).
func leadSentence(lead string) string {
	r, depth := []rune(lead), 0
	for i, c := range r {
		switch c {
		case '(', '[':
			depth++
		case ')', ']':
			depth = max(depth-1, 0)
		case '.', '!', '?', '。', '؟':
			if depth == 0 && (i+1 == len(r) || unicode.IsSpace(r[i+1])) {
				return string(r[:i+1])
			}
		}
	}
	return strings.TrimRightFunc(lead, unicode.IsSpace)
}
