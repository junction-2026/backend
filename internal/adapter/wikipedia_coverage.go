package adapter

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Coverage labels of a WikiLookup (wikipedia-lookup-result "Wikipedia coverage label").
const (
	CoverageNone      = "no_coverage"
	CoverageMentioned = "mentioned"
	CoverageAligned   = "aligned"
	CoverageConflicts = "conflicts"
)

// wikiCoverage labels one WikiLookup against its claim, from the passage, the claim text and its matched entities
// only (no LLM, no other WikiLookup). Call it after matched_entities is set.
func wikiCoverage(c claim, l wikiLookup) (string, *string) {
	if l.Status != "found" || l.Passage == nil {
		return CoverageNone, nil
	}
	var named []string
	for _, e := range l.MatchedEntities {
		if strings.IndexFunc(e, func(r rune) bool { return !isASCIIDigit(r) && !unicode.IsSpace(r) && !unicode.IsPunct(r) }) >= 0 {
			named = append(named, fold(e))
		}
	}
	if len(named) == 0 {
		return CoverageNone, nil
	}
	nums := uniqueRuns(c.Text)
	if len(nums) == 0 {
		return CoverageMentioned, nil
	}
	passage, sentences := *l.Passage, passageSentences(*l.Passage)
	for _, s := range sentences {
		if occursAll(s, nums) {
			return verified(CoverageAligned, passage, s, nums)
		}
	}
	isClaimNum, perKind := map[string]bool{}, map[bool][]string{}
	for _, n := range nums {
		isClaimNum[n] = true
		perKind[len(n) == 4] = append(perKind[len(n) == 4], n)
	}
	for _, s := range sentences {
		fs := fold(s)
		if !containsAny(fs, named) {
			continue
		}
		for _, x := range standaloneNumbers(s) {
			if same := perKind[len(x) == 4]; !isClaimNum[x] && len(same) == 1 && !occursAll(s, same) {
				return verified(CoverageConflicts, passage, s, []string{x})
			}
		}
	}
	return CoverageMentioned, nil
}

// verified re-checks an aligned/conflicts span: a non-empty substring of passage holding the needed numbers, else
// the label downgrades to mentioned.
func verified(label, passage, s string, need []string) (string, *string) {
	if s == "" || !strings.Contains(passage, s) || !occursAll(s, need) {
		return CoverageMentioned, nil
	}
	return label, &s
}

func uniqueRuns(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range digitRuns(s) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// occursAll reports whether every number is a maximal digit run of s.
func occursAll(s string, nums []string) bool {
	in := map[string]bool{}
	for _, n := range digitRuns(s) {
		in[n] = true
	}
	for _, n := range nums {
		if !in[n] {
			return false
		}
	}
	return true
}

func containsAny(folded string, foldedSubs []string) bool {
	for _, e := range foldedSubs {
		if strings.Contains(folded, e) {
			return true
		}
	}
	return false
}

// passageSentences splits after . ! ? … 。！？ followed by whitespace or the end, and at line breaks; parts are
// trimmed, empty parts dropped, so every sentence is a substring of passage.
func passageSentences(passage string) []string {
	var out []string
	start := 0
	cut := func(end, next int) {
		if s := strings.TrimSpace(passage[start:end]); s != "" {
			out = append(out, s)
		}
		start = next
	}
	for i, r := range passage {
		w := utf8.RuneLen(r)
		switch {
		case r == '\n' || r == '\r':
			cut(i, i+w)
		case strings.ContainsRune(".!?…。！？", r):
			if next, _ := utf8.DecodeRuneInString(passage[i+w:]); i+w == len(passage) || unicode.IsSpace(next) {
				cut(i+w, i+w)
			}
		}
	}
	cut(len(passage), len(passage))
	return out
}

// standaloneNumbers are the digit runs of s not joined to another digit run by . , ' NBSP or narrow NBSP
// (parts of 1.500, 1,500 or 2,5 have no known value without locale parsing).
func standaloneNumbers(s string) []string {
	var out []string
	joiner := func(r rune) bool { return strings.ContainsRune(".,'  ", r) }
	for i := 0; i < len(s); {
		if !isASCIIDigit(rune(s[i])) {
			i++
			continue
		}
		j := i
		for j < len(s) && isASCIIDigit(rune(s[j])) {
			j++
		}
		prev, pw := utf8.DecodeLastRuneInString(s[:i])
		before, _ := utf8.DecodeLastRuneInString(s[:i-min(pw, i)])
		next, nw := utf8.DecodeRuneInString(s[j:])
		after, _ := utf8.DecodeRuneInString(s[j+min(nw, len(s)-j):])
		grouped := i > 0 && joiner(prev) && isASCIIDigit(before) || j < len(s) && joiner(next) && isASCIIDigit(after)
		if !grouped {
			out = append(out, s[i:j])
		}
		i = j
	}
	return out
}
