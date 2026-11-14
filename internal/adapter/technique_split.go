package adapter

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const minSentenceWords = 3

// sentence is a fragment of the classified text; Start/End are half-open UTF-16 code-unit offsets in it.
type sentence struct {
	Text       string
	Start, End int
}

// splitSentences splits text at runs of '.', '!', '?' (kept with their sentence) and at newlines, trims
// whitespace and drops fragments of fewer than minSentenceWords words.
// shortcut: no abbreviation or decimal handling ("e.g.", "3.5" split), upgrade if the model needs cleaner sentences.
func splitSentences(text string) []sentence {
	var out []sentence
	u16, from, fromU16 := 0, 0, 0 // fragment start: byte index and UTF-16 offset
	flush := func(to int) {
		frag := text[from:to]
		lead := len(frag) - len(strings.TrimLeftFunc(frag, unicode.IsSpace))
		t := strings.TrimSpace(frag)
		if len(strings.Fields(t)) >= minSentenceWords {
			s := fromU16 + utf16Len(frag[:lead])
			out = append(out, sentence{t, s, s + utf16Len(t)})
		}
	}
	for i := 0; i < len(text); {
		r, n := utf8.DecodeRuneInString(text[i:])
		switch r {
		case '.', '!', '?':
			j, ju := i+n, u16+1
			for j < len(text) && strings.IndexByte(".!?", text[j]) >= 0 {
				j, ju = j+1, ju+1
			}
			flush(j)
			i, u16, from, fromU16 = j, ju, j, ju
			continue
		case '\n':
			flush(i)
			i, u16, from, fromU16 = i+n, u16+1, i+n, u16+1
			continue
		}
		i, u16 = i+n, u16+utf16Len(text[i:i+n])
	}
	flush(len(text))
	return out
}

// utf16Len is the length of s in UTF-16 code units (JavaScript string length).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}
