package adapter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindTermBoundaries(t *testing.T) {
	for name, c := range map[string]struct {
		text, pat string
		start     int // -1: not found
	}{
		"inside a longer word":      {"Europe votes", "EU", -1},
		"prefix of a word":          {"EUR is money", "EU", -1},
		"digit neighbour":           {"EU2 and X", "EU", -1},
		"hyphen is a boundary":      {"an EU-wide rule", "EU", 3},
		"skips bad then finds good": {"Europe, EU.", "EU", 8},
		"case-insensitive":          {"calls for REMIGRATION now", "remigration", 10},
		"unicode letters":           {"Särger und Ärger!", "ärger", 11},
		"cyrillic":                  {"Это ВЭФ в Давосе", "вэф", 4},
		"term with punctuation":     {"the U.S. Army said", "U.S. Army", 4},
		"at the edges":              {"EU", "EU", 0},
		"empty pattern":             {"abc", "", -1},
	} {
		t.Run(name, func(t *testing.T) {
			s, ok := findTerm([]rune(c.text), []rune(c.pat))
			if c.start < 0 {
				require.False(t, ok)
				return
			}
			require.True(t, ok)
			require.Equal(t, c.start, s)
		})
	}
}

func TestHasLetter(t *testing.T) {
	require.False(t, hasLetter("2020"))
	require.False(t, hasLetter("12.5 %"))
	require.True(t, hasLetter("5G"))
	require.True(t, hasLetter("日本"))
}

func inPostOf(text, term string, limit int) string {
	r := []rune(text)
	s, ok := findTerm(r, []rune(term))
	if !ok {
		panic("term not in text")
	}
	return sentenceAround(r, s, s+len([]rune(term)), limit)
}

func TestSentenceAround(t *testing.T) {
	for name, c := range map[string]struct{ text, term, want string }{
		"middle sentence":        {"Prices rose. They call it the Great Reset! Wake up.", "Great Reset", "They call it the Great Reset!"},
		"line breaks":            {"#Remigration jetzt\nTeilen!", "Remigration", "#Remigration jetzt"},
		"line break before":      {"Intro\n  Wake up, Plandemie is here. Next", "Plandemie", "Wake up, Plandemie is here."},
		"question and ellipsis":  {"Really? Is it Agenda 2030… yes. End", "Agenda 2030", "Is it Agenda 2030…"},
		"cjk terminators":        {"価格が上がった。 これが 大リセット だ！ 次の文。", "大リセット", "これが 大リセット だ！"},
		"cjk without whitespace": {"価格が上がった。これが 大リセット だ！", "大リセット", "価格が上がった。これが 大リセット だ！"},
		"arabic question mark":   {"هل هذا؟ هذا هو الكلمة. نعم", "الكلمة", "هذا هو الكلمة."},
		"arabic first sentence":  {"هذا هو الكلمة؟ نعم", "الكلمة", "هذا هو الكلمة؟"},
		"dot inside the term":    {"Nope. He joined the U.S. Army today. Fine.", "U.S. Army", "He joined the U.S. Army today."},
		"decimal is no boundary": {"It costs 3.5 euro for Plandemie tests. Ok", "Plandemie", "It costs 3.5 euro for Plandemie tests."},
		"whole text":             {"  just Plandemie here  ", "Plandemie", "just Plandemie here"},
		"abbreviation boundary":  {"Mr. Smith loves the WEF.", "WEF", "Smith loves the WEF."}, // "Mr. " is a boundary by contract
	} {
		t.Run(name, func(t *testing.T) {
			got := inPostOf(c.text, c.term, 300)
			require.Equal(t, c.want, got)
			require.Contains(t, c.text, got)
			require.Contains(t, strings.ToLower(got), strings.ToLower(c.term))
		})
	}
}

func TestSentenceAroundLongSentenceCut(t *testing.T) {
	long := strings.Repeat("word ", 100) + "Plandemie " + strings.Repeat("more ", 100) + "end."
	for _, limit := range []int{300, 40, 11} {
		got := inPostOf(long, "Plandemie", limit)
		require.LessOrEqual(t, len([]rune(got)), limit)
		require.Contains(t, got, "Plandemie")
		require.Contains(t, long, got)
	}
	got := inPostOf(long, "Plandemie", 40)
	require.False(t, strings.HasPrefix(got, "ord") || strings.HasSuffix(got, "mor"), "cut at word boundaries: %q", got)
	// term at the start and at the end of a long sentence
	require.Contains(t, inPostOf("Plandemie "+strings.Repeat("x ", 400), "Plandemie", 50), "Plandemie")
	require.Contains(t, inPostOf(strings.Repeat("x ", 400)+"Plandemie", "Plandemie", 50), "Plandemie")
}

func TestLeadSentence(t *testing.T) {
	for name, c := range map[string]struct{ lead, want string }{
		"parenthesis": {"The Great Reset (also: Davos agenda.) is an initiative of the World Economic Forum. It was launched in 2020.",
			"The Great Reset (also: Davos agenda.) is an initiative of the World Economic Forum."},
		"square brackets":  {"Foo [a. b] is bar. Baz.", "Foo [a. b] is bar."},
		"abbreviation dot": {"Dr.Müller is here. Next", "Dr.Müller is here."}, // a dot not followed by whitespace is no end
		"question":         {"Is it? Yes.", "Is it?"},
		"cjk full stop":    {"これは本です。 次の文。", "これは本です。"},
		"arabic question":  {"ما هذا؟ نعم", "ما هذا؟"},
		"no terminator":    {"No terminator here  ", "No terminator here"},
		"unbalanced close": {"A ) b. C", "A ) b."},
		"unclosed paren":   {"A (b. C", "A (b. C"},
		"end of lead":      {"Only one.", "Only one."},
		"newline after":    {"First.\nSecond.", "First."},
	} {
		t.Run(name, func(t *testing.T) {
			got := leadSentence(c.lead)
			require.Equal(t, c.want, got)
			require.True(t, strings.HasPrefix(c.lead, got))
		})
	}
}
