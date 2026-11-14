package adapter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func vals(lang, passage string) (out []string) {
	for _, v := range extractValues(lang, passage) {
		out = append(out, v.Kind+":"+v.Value+"="+v.Text)
	}
	return out
}

func TestExtractValues(t *testing.T) {
	for _, tc := range []struct {
		name, lang, passage string
		want                []string
	}{
		{"de textual date", "de", "am 2. März 2025 war es soweit", []string{"date:2025-03-02=2. März 2025"}},
		{"en day month year", "en", "on 2 March 2025 it", []string{"date:2025-03-02=2 March 2025"}},
		{"en month day year", "en", "on March 2, 2025 it", []string{"date:2025-03-02=March 2, 2025"}},
		{"de month year", "de", "im März 2025", []string{"date:2025-03=März 2025"}},
		{"en month year", "en", "in March 2025", []string{"date:2025-03=March 2025"}},
		{"de numeric date", "de", "am 02.03.2025 und 2.3.2025", []string{"date:2025-03-02=02.03.2025"}},
		{"iso date and month", "en", "2025-03-02 and 2025-04", []string{"date:2025-03-02=2025-03-02", "date:2025-04=2025-04"}},
		{"iso in other language", "fr", "le 2025-03-02", []string{"date:2025-03-02=2025-03-02"}},
		{"other language has no month names", "fr", "2 mars 2025", []string{"number:2=2", "number:2025=2025"}},
		{"de grouped", "de", "1.500 Einwohner", []string{"number:1500=1.500"}},
		{"de NBSP and narrow NBSP grouping", "de", "1 500 und 2 500 000", []string{"number:1500=1 500", "number:2500000=2 500 000"}},
		{"en grouped", "en", "1,500 inhabitants", []string{"number:1500=1,500"}},
		{"de decimal comma", "de", "2,50 Euro", []string{"number:2.5=2,50"}},
		{"en decimal point", "en", "0.50 and 007 and 3.0", []string{"number:0.5=0.50", "number:7=007", "number:3=3.0"}},
		{"other language en separators", "fr", "1,500 et 1 500 et 2.5", []string{"number:1500=1,500", "number:2.5=2.5"}},
		{"standalone year", "en", "in 2025", []string{"number:2025=2025"}},
		{"date digits give no number", "de", "2. März 2025 und 7", []string{"date:2025-03-02=2. März 2025", "number:7=7"}},
		{"dedup keeps first text", "de", "67 und 67,0 und 67", []string{"number:67=67"}},
		{"no digits", "de", "Die Partei wurde gegründet.", []string{}},
		{"invalid month is not an iso date", "en", "2025-13", []string{"number:2025=2025", "number:13=13"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := vals(tc.lang, tc.passage)
			if got == nil {
				got = []string{}
			}
			require.Equal(t, tc.want, got)
		})
	}
}

func crossLookup(lang, status, passage string) wikiLookup {
	u := "https://" + lang + ".wikipedia.org/x"
	l := wikiLookup{Language: lang, Status: status, URL: &u}
	if passage != "" {
		l.Passage = &passage
	}
	return l
}

func TestCrossLanguage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		wikis []wikiLookup
		want  string // "" for null
		diffs []crossLangDiff
	}{
		{"agree", []wikiLookup{crossLookup("de", "found", "Das Rentenalter liegt seit 2012 bei 67 Jahren."), crossLookup("en", "found", "Since 2012 the retirement age has been 67.")}, CrossAgree, nil},
		{"agree same date and grouped number", []wikiLookup{crossLookup("de", "found", "Am 2. März 2025: 1.500"), crossLookup("en", "found", "On 2 March 2025: 1,500")}, CrossAgree, nil},
		{"agree without values", []wikiLookup{crossLookup("de", "found", "Eine Partei."), crossLookup("en", "found", "A party.")}, CrossAgree, nil},
		{"discrepancy", []wikiLookup{crossLookup("de", "found", "Das Alter liegt seit 2012 bei 67 Jahren."), crossLookup("en", "found", "Since 2012 the age is 70.")}, CrossDiscrepancy, []crossLangDiff{
			{"de", "https://de.wikipedia.org/x", []comparedValue{{"number", "67", "67"}}},
			{"en", "https://en.wikipedia.org/x", []comparedValue{{"number", "70", "70"}}}}},
		{"discrepancy one side empty", []wikiLookup{crossLookup("de", "found", "Die Partei wurde gegründet."), crossLookup("en", "found", "The party was founded in 1945.")}, CrossDiscrepancy, []crossLangDiff{
			{"de", "https://de.wikipedia.org/x", []comparedValue{}},
			{"en", "https://en.wikipedia.org/x", []comparedValue{{"number", "1945", "1945"}}}}},
		{"year only against month year", []wikiLookup{crossLookup("de", "found", "im März 2025"), crossLookup("en", "found", "in 2025")}, CrossDiscrepancy, []crossLangDiff{
			{"de", "https://de.wikipedia.org/x", []comparedValue{{"date", "2025-03", "März 2025"}}},
			{"en", "https://en.wikipedia.org/x", []comparedValue{{"number", "2025", "2025"}}}}},
		{"gap no_page", []wikiLookup{crossLookup("de", "no_page", ""), crossLookup("en", "found", "In 2025.")}, CrossGap, nil},
		{"gap ambiguous", []wikiLookup{crossLookup("de", "found", "Im Jahr 2025."), crossLookup("en", "ambiguous", "")}, CrossGap, nil},
		{"single wiki", []wikiLookup{crossLookup("en", "found", "In 2025.")}, "", nil},
		{"both missing", []wikiLookup{crossLookup("de", "no_page", ""), crossLookup("en", "ambiguous", "")}, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]wikiLookup(nil), tc.wikis...)
			state, diffs := crossLanguage(tc.wikis)
			require.Equal(t, before, tc.wikis) // WikiLookups untouched
			if tc.want == "" {
				require.Nil(t, state)
			} else {
				require.Equal(t, tc.want, *state)
			}
			require.Equal(t, tc.diffs, diffs)
		})
	}
}
