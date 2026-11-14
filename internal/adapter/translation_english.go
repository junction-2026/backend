package adapter

import (
	"regexp"
	"strings"
	"unicode"
)

// englishVerdict is the outcome of the local English check.
type englishVerdict int

const (
	verdictUnsure englishVerdict = iota
	verdictEnglish
	verdictNotEnglish
)

// A function-word list per language. shortcut: tiny stop-word heuristic, not a
// language identifier; it only has to be sure when it says english.
var (
	englishWords = wordSet(`a an the and or but if of to in on at by for with from about as into over after before up down out
		off than then so not no is are was were be been am do does did have has had will would can could should may might must
		i you he she it we they me him her us them my your his its our their this that these those who what which when where
		why how there here all some any only very just too also don't it's you're they're we're i'm that's isn't can't won't
		until while because more most other such`)
	otherWords = map[string]map[string]bool{
		"de": wordSet(`der die das den dem des ein eine einen und oder aber nicht ist sind war sein ich du er sie es wir ihr euch uns
			mit von zu für auf bei nach aus im um auch nur noch wie wenn dass bevor alle vor über wird hat kein`),
		"fr": wordSet(`le la les un une des du de et ou mais ne pas est sont être je tu il elle nous vous ils elles ce cette ces que
			qui dans pour sur avec par au aux en plus très son sa ses leur mon ma nos vos où comme tout`),
		"es": wordSet(`el la los las un una unos unas y o pero no es son está están yo tú él ella nosotros ellos que de del en con por
			para sin sobre se su sus mi lo al como más muy este esta esto ya si también`),
		"it": wordSet(`il lo la le gli un uno una e o ma non è sono io tu lui lei noi voi loro che di del della in con per su tra fra
			da al alla nel nella come più molto questo questa anche se mi ti ci si cosa`),
		"nl": wordSet(`de het een en of maar niet is zijn was ik jij je hij zij ze wij jullie dat die dit deze van voor met op aan bij
			naar uit door om te ook nog wel geen als dan heeft hebben wordt over`),
		"pt": wordSet(`o a os as um uma uns e ou mas não é são está foram eu tu ele ela nós eles elas que de do da dos das em no na nos
			nas com por para sem sobre se seu sua mais muito este esta isso também como já`),
	}
	noiseRE = regexp.MustCompile(`(?i)(https?://|www\.)\S+|@[\p{L}\p{N}_.]+|#[\p{L}\p{N}_]+`)
)

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// textWords lower-cases text, drops URLs, @mentions and #hashtags and returns the maximal runs of
// letters and apostrophes that contain a letter.
func textWords(text string) []string {
	text = noiseRE.ReplaceAllString(strings.ToLower(text), " ")
	var words []string
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' && r != '’' }) {
		if strings.IndexFunc(f, unicode.IsLetter) >= 0 {
			words = append(words, f)
		}
	}
	return words
}

// detectEnglish says english only when sure; it is biased toward unsure so Mistral is called when in doubt.
func detectEnglish(text string, minWords int, minRatio float64) englishVerdict {
	words := textWords(text)
	if len(words) < minWords {
		return verdictUnsure
	}
	letters, latin := 0, 0
	for _, w := range words {
		for _, r := range w {
			if unicode.IsLetter(r) {
				letters++
				if unicode.Is(unicode.Latin, r) {
					latin++
				}
			}
		}
	}
	if float64(letters-latin) > 0.1*float64(letters) {
		return verdictNotEnglish
	}
	n := float64(len(words))
	count := func(set map[string]bool) float64 {
		c := 0
		for _, w := range words {
			if set[strings.Trim(strings.ReplaceAll(w, "’", "'"), "'")] {
				c++
			}
		}
		return float64(c) / n
	}
	en, other := count(englishWords), 0.0
	for _, set := range otherWords {
		other = max(other, count(set))
	}
	switch {
	case en >= minRatio && en >= 2*other:
		return verdictEnglish
	case other >= 0.20 && other > en:
		return verdictNotEnglish
	}
	return verdictUnsure
}
