package adapter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func found(c claim, passage string) wikiLookup {
	return personalize(wikiLookup{Language: "en", Status: "found", Passage: &passage}, c, "q", false)
}

func TestWikiCoverageLabels(t *testing.T) {
	for _, tc := range []struct {
		name, text, passage string
		entities            []string
		want, evidence      string
	}{
		{"no number is mentioned", "Angela Merkel was a member of the CDU.", "Angela Merkel led the CDU.",
			[]string{"Angela Merkel", "CDU"}, CoverageMentioned, ""},
		{"number shared but no entity", "Germany raised the age to 67 in 2007.", "In 2007 the age became 67.",
			[]string{"Germany"}, CoverageNone, ""},
		{"numeric entity is not a named entity", "Pension age 70.", "It is 70.", []string{"70"}, CoverageNone, ""},
		{"aligned", "Germany raised the retirement age to 67 in 2007.",
			"In 2007, Germany decided to raise the retirement age to 67 by 2029. The change took effect gradually.",
			[]string{"Germany", "retirement age"}, CoverageAligned, "In 2007, Germany decided to raise the retirement age to 67 by 2029."},
		{"conflicts on a year", "Angela Merkel became chancellor in 2006.",
			"Angela Merkel is a German politician who was Chancellor of Germany from 2005 to 2021. She led the CDU.",
			[]string{"Angela Merkel"}, CoverageConflicts, "Angela Merkel is a German politician who was Chancellor of Germany from 2005 to 2021."},
		{"conflicts on a count", "The pension age in Germany is 70.", "In Germany the pension age is 67.",
			[]string{"Germany", "pension age"}, CoverageConflicts, "In Germany the pension age is 67."},
		{"mentioned without the number", "The government raised the pension age to 70 in 2025.",
			"In many countries the pension age is 67; some plan 70.",
			[]string{"government", "pension age", "70"}, CoverageMentioned, ""},
		{"two counts are not compared", "Today the Bundestag voted 412 to 180 to ban cash payments.",
			"The Bundestag is the German federal parliament. It has 630 members.", []string{"Bundestag"}, CoverageMentioned, ""},
		{"grouped number never conflicts", "Die Stadt hat 1500 Einwohner.", "Die Stadt hat 1.500 Einwohner.",
			[]string{"Stadt"}, CoverageMentioned, ""},
		{"decimal and NBSP grouping never conflict", "Die Stadt hat 3 Parks.", "Die Stadt hat 2,5 km² und 1 500 Bäume.",
			[]string{"Stadt"}, CoverageMentioned, ""},
		{"grouped digit runs align", "Die Stadt hat 1,500 Einwohner.", "Die Stadt hat 1,500 Einwohner.",
			[]string{"Stadt"}, CoverageAligned, "Die Stadt hat 1,500 Einwohner."},
		{"conflict needs the entity in the sentence", "Angela Merkel became chancellor in 2006.",
			"Angela Merkel is a politician.\nThe office existed in 1949.", []string{"Angela Merkel"}, CoverageMentioned, ""},
		// only digit runs are compared: 05 is a count other than the claim's single count 3
		{"dates compared by digit runs", "The law passed on 3 May 2025.", "The law took effect 2025-05-03.",
			[]string{"law"}, CoverageConflicts, "The law took effect 2025-05-03."},
		{"words are not numbers", "The pension age is 70.", "The pension age is seventy.", []string{"pension age"}, CoverageMentioned, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := found(claim{Text: tc.text, Entities: tc.entities}, tc.passage)
			require.Equal(t, tc.want, l.Coverage)
			if tc.evidence == "" {
				require.Nil(t, l.EvidenceSentence)
			} else {
				require.Equal(t, tc.evidence, *l.EvidenceSentence)
			}
		})
	}
}

func TestWikiCoverageNotFound(t *testing.T) {
	for _, st := range []string{"no_page", "ambiguous"} {
		cov, ev := wikiCoverage(pensClaim, wikiLookup{Status: st})
		require.Equal(t, CoverageNone, cov)
		require.Nil(t, ev)
	}
}

func TestWikiCoverageTodayEvent(t *testing.T) {
	c := claim{Text: "Today the Bundestag voted 412 to 180 to ban cash payments.", Entities: []string{"Bundestag"}}
	cov, _ := wikiCoverage(c, wikiLookup{Status: "no_page"})
	require.Equal(t, CoverageNone, cov)
	require.Equal(t, CoverageMentioned, found(c, "The Bundestag is the German federal parliament.").Coverage)
}

func TestPassageSentences(t *testing.T) {
	require.Equal(t, []string{"Angela Merkel is a German politician.", "She was Chancellor of Germany from 2005 to 2021.", "She led the CDU."},
		passageSentences("Angela Merkel is a German politician. She was Chancellor of Germany from 2005 to 2021.\nShe led the CDU."))
	require.Equal(t, []string{"Es kostet 2.5 Euro!", "Wirklich?", "Ja…", "东京。", "大阪！"},
		passageSentences("  Es kostet 2.5 Euro! Wirklich?\r\n\nJa… 东京。 大阪！"))
	require.Nil(t, passageSentences(" \n "))
}

func TestStandaloneNumbers(t *testing.T) {
	require.Equal(t, []string{"2005", "2021"}, standaloneNumbers("from 2005 to 2021."))
	require.Nil(t, standaloneNumbers("1.500 1,500 2,5 1'000 1 000 1 000"))
	require.Equal(t, []string{"7", "8"}, standaloneNumbers("7, 8."))
}

// The downgrade rule: a span that is not verbatim in passage or lacks the number never yields aligned/conflicts.
func TestWikiCoverageDowngrade(t *testing.T) {
	p := "In Germany the pension age is 67."
	for _, tc := range []struct {
		span string
		need []string
		want string
	}{
		{"In Germany the pension age is 67.", []string{"67"}, CoverageConflicts},
		{"In Germany the pension age is 67 .", []string{"67"}, CoverageMentioned},
		{"In Germany the pension age is 67.", []string{"70"}, CoverageMentioned},
		{"", nil, CoverageMentioned},
	} {
		got, ev := verified(CoverageConflicts, p, tc.span, tc.need)
		require.Equal(t, tc.want, got, tc.span)
		require.Equal(t, got == CoverageMentioned, ev == nil)
	}
}

// Cached and uncached lookups give the same label; a cached answer is relabelled for the current claim.
func TestWikiCoverageCacheParity(t *testing.T) {
	s := newWikiStub(t, map[string]string{"en|Angela Merkel": "merkel_2005"})
	dir := t.TempDir()
	c := wc("Angela Merkel became chancellor in 2006.", "Angela Merkel", "Angela Merkel", 0.8, "Angela Merkel")
	first := runWiki(t, &Executor{}, wikiDef(s.URL, dir, nil), wikiRequest("en", c)).Claims[0].Wikis[0]
	second := runWiki(t, &Executor{}, wikiDef(s.URL, dir, nil), wikiRequest("en", c)).Claims[0].Wikis[0]
	require.False(t, first.Cached)
	require.True(t, second.Cached)
	require.Equal(t, CoverageConflicts, first.Coverage)
	require.Equal(t, first.Coverage, second.Coverage)
	require.Equal(t, *first.EvidenceSentence, *second.EvidenceSentence)
	require.Contains(t, *first.Passage, *first.EvidenceSentence)

	c.Text = "Angela Merkel became chancellor in 2005."
	third := runWiki(t, &Executor{}, wikiDef(s.URL, dir, nil), wikiRequest("en", c)).Claims[0].Wikis[0]
	require.True(t, third.Cached)
	require.Equal(t, CoverageAligned, third.Coverage)
}
