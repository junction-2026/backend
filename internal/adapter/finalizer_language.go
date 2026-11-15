package adapter

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/junction-2026/backend/internal/registry"
)

// languageNames maps ISO 639-1 codes to the English names put in the finalizer's <source_language> element.
// shortcut: a fixed table of common languages; a valid code outside it is sent as en (English), extend when needed.
var languageNames = map[string]string{
	"en": "English", "de": "German", "fr": "French", "es": "Spanish", "it": "Italian", "nl": "Dutch", "pt": "Portuguese",
	"ru": "Russian", "uk": "Ukrainian", "pl": "Polish", "cs": "Czech", "sk": "Slovak", "sl": "Slovenian", "hr": "Croatian",
	"sr": "Serbian", "bg": "Bulgarian", "ro": "Romanian", "hu": "Hungarian", "el": "Greek", "tr": "Turkish", "fi": "Finnish",
	"sv": "Swedish", "da": "Danish", "no": "Norwegian", "nb": "Norwegian", "is": "Icelandic", "et": "Estonian", "lv": "Latvian",
	"lt": "Lithuanian", "ar": "Arabic", "he": "Hebrew", "fa": "Persian", "hi": "Hindi", "bn": "Bengali", "ur": "Urdu",
	"ta": "Tamil", "zh": "Chinese", "ja": "Japanese", "ko": "Korean", "vi": "Vietnamese", "th": "Thai", "id": "Indonesian",
	"ms": "Malay", "tl": "Tagalog", "sw": "Swahili", "ca": "Catalan", "ga": "Irish", "af": "Afrikaans",
}

// FinalizerLanguage resolves a validated source language code to the code and English name sent to the finalizer;
// an empty code or one missing from the table is en (English).
func FinalizerLanguage(code string) (string, string) {
	if n, ok := languageNames[code]; ok {
		return code, n
	}
	return "en", "English"
}

// languageSets are the word lists of the languages detectLanguage tells apart.
func languageSets() map[string]map[string]bool {
	m := map[string]map[string]bool{"en": englishWords}
	for k, v := range otherWords {
		m[k] = v
	}
	return m
}

// detectLanguage names the language of text among en, de, fr, es, it, nl and pt, or "" when it is not sure. It
// applies the thresholds of detectEnglish: at least minWords words, mostly Latin letters, and the best language
// reaching minRatio of the words and twice the ratio of every other one. shortcut: stop-word heuristic like
// detectEnglish; upgrade to a real identifier if soft false negatives matter.
func detectLanguage(text string, minWords int, minRatio float64) string {
	words := textWords(text)
	if len(words) < minWords {
		return ""
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
		return ""
	}
	best, bestR, second := "", 0.0, 0.0
	for lang, set := range languageSets() {
		c := 0
		for _, w := range words {
			if set[strings.Trim(strings.ReplaceAll(w, "’", "'"), "'")] {
				c++
			}
		}
		switch r := float64(c) / float64(len(words)); {
		case r > bestR:
			best, bestR, second = lang, r, bestR
		case r > second:
			second = r
		}
	}
	if bestR >= minRatio && bestR >= 2*second {
		return best
	}
	return ""
}

// WrongLanguage reports whether the user_text of a finalizer reply is confidently in another language than
// expected (an ISO 639-1 code). It is false for an expected language the detector does not know, short or mixed
// text and a reply without a user_text string.
func WrongLanguage(expected string, reply json.RawMessage) bool {
	if languageSets()[expected] == nil {
		return false
	}
	var r struct {
		UserText string `json:"user_text"`
	}
	if json.Unmarshal(reply, &r) != nil {
		return false
	}
	got := detectLanguage(r.UserText, registry.DefaultEnglishMinWords, registry.DefaultEnglishRatio)
	return got != "" && got != expected
}
