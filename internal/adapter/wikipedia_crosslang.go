package adapter

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Cross-language states of a ClaimLookup (wikipedia-lookup-result "Cross-language state").
const (
	CrossAgree       = "agree"
	CrossDiscrepancy = "discrepancy"
	CrossGap         = "gap"
)

// comparedValue is a date or number read from a passage; Text is the verbatim passage substring.
type comparedValue struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Text  string `json:"text"`
}

type crossLangDiff struct {
	Language string          `json:"language"`
	URL      string          `json:"url"`
	Values   []comparedValue `json:"values"`
}

const (
	sp      = `[\s\x{00A0}\x{202F}]+`
	dayRe   = `(?P<d>0?[1-9]|[12]\d|3[01])`
	y4      = `(?P<y>\d{4})`
	yearRe  = y4 + `\b`
	isoMon  = `(?P<m>0[1-9]|1[0-2])`
	isoDay  = `(?P<d>0[1-9]|[12]\d|3[01])`
	deMonRe = `(?P<mon>Januar|Jänner|Februar|März|April|Mai|Juni|Juli|August|September|Oktober|November|Dezember)`
	enMonRe = `(?P<mon>January|February|March|April|May|June|July|August|September|October|November|December)`
)

var monthNames = func() map[string]int {
	m := map[string]int{"Jänner": 1}
	for i, n := range strings.Split("Januar Februar März April Mai Juni Juli August September Oktober November Dezember", " ") {
		m[n] = i + 1
	}
	for i, n := range strings.Split("January February March April May June July August September October November December", " ") {
		m[n] = i + 1
	}
	return m
}()

// valueRule reads one notation: a date (groups y, m|mon, optional d) or, without groups, a number.
type valueRule struct {
	re     *regexp.Regexp
	number func(string) string // set for the number rule
}

func rule(p string) valueRule { return valueRule{re: regexp.MustCompile(p)} }

func numRule(p string, group, dec string) valueRule {
	return valueRule{re: regexp.MustCompile(p), number: func(s string) string { return plainDecimal(s, group, dec) }}
}

var isoRules = []valueRule{rule(y4 + `-` + isoMon + `-` + isoDay + `\b`), rule(y4 + `-` + isoMon + `\b`)}

// rulesFor lists the rules of a wiki language in priority order (at one position the first rule wins).
// shortcut: de/en dates only, other languages ISO 8601; add a rule per language here when needed.
func rulesFor(lang string) []valueRule {
	switch lang {
	case "de":
		return append(slices.Clone(isoRules),
			rule(dayRe+`\.`+`(?P<m>0?[1-9]|1[0-2])\.`+yearRe),
			rule(dayRe+`\.?`+sp+deMonRe+sp+yearRe),
			rule(deMonRe+sp+yearRe),
			numRule(`\d+(?:[.\x{00A0}\x{202F}]\d{3})*(?:,\d+)?`, ".  ", ","))
	case "en":
		return append(slices.Clone(isoRules),
			rule(enMonRe+sp+dayRe+`,?`+sp+yearRe),
			rule(dayRe+sp+enMonRe+sp+yearRe),
			rule(enMonRe+sp+yearRe),
			numRule(`\d+(?:,\d{3})*(?:\.\d+)?`, ",", "."))
	}
	return append(slices.Clone(isoRules), numRule(`\d+(?:[,\x{00A0}\x{202F}]\d{3})*(?:\.\d+)?`, ",  ", "."))
}

// plainDecimal drops the group separators, uses "." as decimal separator and trims leading and trailing zeros.
func plainDecimal(s, group, dec string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(group, r) {
			return -1
		}
		return r
	}, s)
	i, f, _ := strings.Cut(s, dec)
	i = strings.TrimLeft(i, "0")
	if i == "" {
		i = "0"
	}
	if f = strings.TrimRight(f, "0"); f != "" {
		return i + "." + f
	}
	return i
}

// dateValue builds YYYY-MM[-DD] from the named groups of a date match.
func dateValue(re *regexp.Regexp, sub []string) string {
	g := func(n string) string { return sub[re.SubexpIndex(n)] }
	m := 0
	if i := re.SubexpIndex("mon"); i >= 0 {
		m = monthNames[sub[i]]
	} else {
		m, _ = strconv.Atoi(g("m"))
	}
	v := g("y") + "-" + pad2(m)
	if re.SubexpIndex("d") >= 0 {
		d, _ := strconv.Atoi(g("d"))
		v += "-" + pad2(d)
	}
	return v
}

func pad2(n int) string { return strconv.Itoa(100 + n)[1:] }

// extractValues lists the dates and numbers of passage in order of first appearance, deduplicated by (kind, value).
// The digits of a recognised date give no number. lang selects the notations (see rulesFor).
func extractValues(lang, passage string) []comparedValue {
	rules, out, seen := rulesFor(lang), []comparedValue{}, map[[2]string]bool{}
	for pos := 0; pos < len(passage); {
		best, bi := []int(nil), -1
		for i, r := range rules {
			if m := r.re.FindStringSubmatchIndex(passage[pos:]); m != nil && (best == nil || m[0] < best[0]) {
				best, bi = m, i
			}
		}
		if best == nil {
			break
		}
		text, r := passage[pos+best[0]:pos+best[1]], rules[bi]
		v := comparedValue{Kind: "date", Text: text}
		if r.number != nil {
			v.Kind, v.Value = "number", r.number(text)
		} else {
			v.Value = dateValue(r.re, r.re.FindStringSubmatch(text))
		}
		if k := [2]string{v.Kind, v.Value}; !seen[k] {
			seen[k] = true
			out = append(out, v)
		}
		pos += best[1]
	}
	return out
}

// crossLanguage compares the passages of the two WikiLookups of one claim: a state (nil when there is nothing
// to compare) and, for a discrepancy, the values found only in each passage. Pure: reads status, passage,
// language and url only.
func crossLanguage(wikis []wikiLookup) (*string, []crossLangDiff) {
	if len(wikis) != 2 {
		return nil, nil
	}
	page := func(w wikiLookup) bool { return w.Status == "found" && w.Passage != nil }
	state := func(s string) *string { return &s }
	switch a, b := page(wikis[0]), page(wikis[1]); {
	case !a && !b:
		return nil, nil
	case a != b:
		return state(CrossGap), nil
	}
	vals := [2][]comparedValue{extractValues(wikis[0].Language, *wikis[0].Passage), extractValues(wikis[1].Language, *wikis[1].Passage)}
	only := func(from, other []comparedValue) []comparedValue {
		out := []comparedValue{}
		for _, v := range from {
			if !slices.ContainsFunc(other, func(o comparedValue) bool { return o.Kind == v.Kind && o.Value == v.Value }) {
				out = append(out, v)
			}
		}
		return out
	}
	d0, d1 := only(vals[0], vals[1]), only(vals[1], vals[0])
	if len(d0) == 0 && len(d1) == 0 {
		return state(CrossAgree), nil
	}
	url := func(w wikiLookup) string {
		if w.URL == nil {
			return ""
		}
		return *w.URL
	}
	return state(CrossDiscrepancy), []crossLangDiff{
		{Language: wikis[0].Language, URL: url(wikis[0]), Values: d0},
		{Language: wikis[1].Language, URL: url(wikis[1]), Values: d1},
	}
}
